package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"html/template"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxTitleLength       = 30
	maxDescriptionLength = 200
	maxUserNameLength    = 50
	minCost              = 0.1
	budgetTabID          = "budget-tab"
	usersTabID           = "users-tab"
)

type Item struct {
	ID             int
	Title          string
	Description    string
	Cost           float64
	Participants   []string
	ParticipantIDs []int
	Settled        bool
}

type Payment struct {
	ItemID int
	User   string
	Amount float64
}

type User struct {
	ID   int
	Name string
}

func main() {
	dbPath := buildDBPath()
	store, err := newSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("opening sqlite store: %v", err)
	}
	defer store.Close()

	funcMap := template.FuncMap{
		"perShare":       perShare,
		"formatBalance":  formatBalance,
		"formatCurrency": formatCurrency,
	}
	tmpl := template.Must(template.New("index.html").Funcs(funcMap).ParseFiles(filepath.Join("templates", "index.html")))
	editTmpl := template.Must(template.ParseFiles(filepath.Join("templates", "item_edit.html")))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		items, err := store.Items()
		if err != nil {
			http.Error(w, "loading items", http.StatusInternalServerError)
			log.Printf("loading items: %v", err)
			return
		}
		payments, err := store.Payments()
		if err != nil {
			http.Error(w, "loading payments", http.StatusInternalServerError)
			log.Printf("loading payments: %v", err)
			return
		}
		users, err := store.Users()
		if err != nil {
			http.Error(w, "loading users", http.StatusInternalServerError)
			log.Printf("loading users: %v", err)
			return
		}
		participantMap := make(map[int][]string, len(items))
		itemCosts := make(map[int]float64, len(items))
		for _, item := range items {
			itemCosts[item.ID] = item.Cost
			if len(item.Participants) == 0 {
				continue
			}
			names := append([]string(nil), item.Participants...)
			participantMap[item.ID] = names
		}
		participantJSON, err := json.Marshal(participantMap)
		if err != nil {
			http.Error(w, "encoding participants", http.StatusInternalServerError)
			log.Printf("encoding participants: %v", err)
			return
		}
		costJSON, err := json.Marshal(itemCosts)
		if err != nil {
			http.Error(w, "encoding item costs", http.StatusInternalServerError)
			log.Printf("encoding item costs: %v", err)
			return
		}
		paymentsByItem := map[int][]Payment{}
		for _, p := range payments {
			paymentsByItem[p.ItemID] = append(paymentsByItem[p.ItemID], p)
		}
		balances := computeBalances(items, payments)
		settlements := computeSettlements(balances)
		activeTab := normalizeTab(r.URL.Query().Get("tab"))
		warning := r.URL.Query().Get("userWarning")
		renderTemplate(w, tmpl, items, paymentsByItem, balances, settlements, users, warning, activeTab, template.JS(participantJSON), template.JS(costJSON))
	})

	mux.HandleFunc("/item", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not read form", http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}
		if len(title) > maxTitleLength {
			http.Error(w, fmt.Sprintf("title must be at most %d characters", maxTitleLength), http.StatusBadRequest)
			return
		}
		description := strings.TrimSpace(r.FormValue("description"))
		if len(description) > maxDescriptionLength {
			http.Error(w, fmt.Sprintf("description must be at most %d characters", maxDescriptionLength), http.StatusBadRequest)
			return
		}
		cost, err := parseCost(r.FormValue("cost"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		participantIDs := uniqueInts(parseIDs(r.Form["participants"]))
		if len(participantIDs) == 0 {
			http.Error(w, "select at least one participant", http.StatusBadRequest)
			return
		}
		if err := store.validateUserIDs(participantIDs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := store.AddItem(title, description, cost, participantIDs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("adding item: %v", err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	mux.HandleFunc("/payment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			redirectWithWarning(w, r, "could not read form", budgetTabID)
			return
		}
		itemID, err := strconv.Atoi(r.FormValue("item"))
		if err != nil || itemID <= 0 {
			redirectWithWarning(w, r, "invalid item", budgetTabID)
			return
		}
		payer := strings.TrimSpace(r.FormValue("user"))
		if payer == "" {
			redirectWithWarning(w, r, "payer is required", budgetTabID)
			return
		}
		amount, err := parseCost(r.FormValue("amount"))
		if err != nil {
			redirectWithWarning(w, r, err.Error(), budgetTabID)
			return
		}
		item, err := store.ItemByID(itemID)
		if err != nil {
			redirectWithWarning(w, r, "item not found", budgetTabID)
			return
		}
		if amount > item.Cost {
			redirectWithWarning(w, r, fmt.Sprintf("amount cannot exceed the item cost of %s", formatCurrency(item.Cost)), budgetTabID)
			return
		}
		if err := store.AddPayment(itemID, payer, amount); err != nil {
			redirectWithWarning(w, r, err.Error(), budgetTabID)
			log.Printf("recording payment: %v", err)
			return
		}
		redirectToTab(w, r, budgetTabID)
	})

	mux.HandleFunc("/item/edit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil || id <= 0 {
			http.Error(w, "invalid item", http.StatusBadRequest)
			return
		}
		item, err := store.ItemByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		users, err := store.Users()
		if err != nil {
			http.Error(w, "loading users", http.StatusInternalServerError)
			return
		}
		renderItemEditPage(w, editTmpl, item, users, selectedMap(item.ParticipantIDs), fmt.Sprintf("%.2f", item.Cost), "")
	})

	mux.HandleFunc("/item/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not read form", http.StatusBadRequest)
			return
		}
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil || id <= 0 {
			http.Error(w, "invalid item", http.StatusBadRequest)
			return
		}
		users, err := store.Users()
		if err != nil {
			http.Error(w, "loading users", http.StatusInternalServerError)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		description := strings.TrimSpace(r.FormValue("description"))
		rawCost := strings.TrimSpace(r.FormValue("cost"))
		cost, costErr := parseCost(rawCost)
		participantIDs := uniqueInts(parseIDs(r.Form["participants"]))
		selected := selectedMap(participantIDs)
		paidUp := r.FormValue("paid_up") == "1"
		inputItem := &Item{
			ID:             id,
			Title:          title,
			Description:    description,
			ParticipantIDs: participantIDs,
			Settled:        paidUp,
		}
		if title == "" {
			renderItemEditPage(w, editTmpl, inputItem, users, selected, rawCost, "title is required")
			return
		}
		if len(title) > maxTitleLength {
			renderItemEditPage(w, editTmpl, inputItem, users, selected, rawCost, fmt.Sprintf("title must be at most %d characters", maxTitleLength))
			return
		}
		if len(description) > maxDescriptionLength {
			renderItemEditPage(w, editTmpl, inputItem, users, selected, rawCost, fmt.Sprintf("description must be at most %d characters", maxDescriptionLength))
			return
		}
		if costErr != nil {
			renderItemEditPage(w, editTmpl, inputItem, users, selected, rawCost, costErr.Error())
			return
		}
		inputItem.Cost = cost
		if err := store.UpdateItem(id, title, description, cost, participantIDs, paidUp); err != nil {
			renderItemEditPage(w, editTmpl, inputItem, users, selected, fmt.Sprintf("%.2f", cost), err.Error())
			return
		}
		redirectToTab(w, r, budgetTabID)
	})

	mux.HandleFunc("/item/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not read form", http.StatusBadRequest)
			return
		}
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil || id <= 0 {
			http.Error(w, "invalid item", http.StatusBadRequest)
			return
		}
		item, err := store.ItemByID(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if !item.Settled {
			users, err := store.Users()
			if err != nil {
				http.Error(w, "loading users", http.StatusInternalServerError)
				return
			}
			renderItemEditPage(w, editTmpl, item, users, selectedMap(item.ParticipantIDs), fmt.Sprintf("%.2f", item.Cost), "mark the item as paid up before deleting")
			return
		}
		if err := store.DeleteItem(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		redirectToTab(w, r, budgetTabID)
	})

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			redirectWithWarning(w, r, "could not read form", usersTabID)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			redirectWithWarning(w, r, "name is required", usersTabID)
			return
		}
		if len(name) > maxUserNameLength {
			redirectWithWarning(w, r, fmt.Sprintf("name must be at most %d characters", maxUserNameLength), usersTabID)
			return
		}
		if _, err := store.AddUser(name); err != nil {
			redirectWithWarning(w, r, err.Error(), usersTabID)
			log.Printf("adding user: %v", err)
			return
		}
		redirectToTab(w, r, usersTabID)
	})

	mux.HandleFunc("/user/edit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			redirectWithWarning(w, r, "could not read form", usersTabID)
			return
		}
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil || id <= 0 {
			redirectWithWarning(w, r, "invalid user", usersTabID)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			redirectWithWarning(w, r, "name is required", usersTabID)
			return
		}
		if len(name) > maxUserNameLength {
			redirectWithWarning(w, r, fmt.Sprintf("name must be at most %d characters", maxUserNameLength), usersTabID)
			return
		}
		if err := store.UpdateUser(id, name); err != nil {
			redirectWithWarning(w, r, err.Error(), usersTabID)
			log.Printf("updating user: %v", err)
			return
		}
		redirectToTab(w, r, usersTabID)
	})

	mux.HandleFunc("/user/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			redirectWithWarning(w, r, "could not read form", usersTabID)
			return
		}
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil || id <= 0 {
			redirectWithWarning(w, r, "invalid user", usersTabID)
			return
		}
		if err := store.DeleteUser(id); err != nil {
			redirectWithWarning(w, r, err.Error(), usersTabID)
			log.Printf("deleting user: %v", err)
			return
		}
		redirectToTab(w, r, usersTabID)
	})

	addr := ":8080"
	log.Printf("server listening %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, items []*Item, payments map[int][]Payment, balances map[string]float64, settlements []string, users []User, warning string, activeTab string, participants template.JS, itemCosts template.JS) {
	balanceList := make([]struct {
		Name    string
		Balance float64
	}, 0, len(balances))
	for name, bal := range balances {
		balanceList = append(balanceList, struct {
			Name    string
			Balance float64
		}{name, bal})
	}
	sort.Slice(balanceList, func(i, j int) bool { return balanceList[i].Name < balanceList[j].Name })

	data := struct {
		Items       []*Item
		Payments    map[int][]Payment
		Balances    map[string]float64
		BalanceList []struct {
			Name    string
			Balance float64
		}
		Settlements  []string
		Users        []User
		UserWarning  string
		ActiveTab    string
		Participants template.JS
		ItemCosts    template.JS
	}{
		Items:        items,
		Payments:     payments,
		Balances:     balances,
		BalanceList:  balanceList,
		Settlements:  settlements,
		Users:        users,
		UserWarning:  warning,
		ActiveTab:    activeTab,
		Participants: participants,
		ItemCosts:    itemCosts,
	}
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func parseIDs(values []string) []int {
	ids := make([]int, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func parseCost(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("cost is required")
	}
	cost, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cost")
	}
	if cost < minCost {
		return 0, fmt.Errorf("cost must be at least %.2f", minCost)
	}
	return cost, nil
}

func perShare(item *Item) float64 {
	if item == nil || len(item.Participants) == 0 {
		return 0
	}
	return item.Cost / float64(len(item.Participants))
}

func formatBalance(balance float64) string {
	if balance >= 0 {
		return fmt.Sprintf("+$%.2f", balance)
	}
	return fmt.Sprintf("-$%.2f", math.Abs(balance))
}

func formatCurrency(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

func computeBalances(items []*Item, payments []Payment) map[string]float64 {
	balances := map[string]float64{}
	for _, item := range items {
		if len(item.Participants) == 0 {
			continue
		}
		share := item.Cost / float64(len(item.Participants))
		for _, p := range item.Participants {
			balances[p] -= share
		}
	}
	for _, payment := range payments {
		balances[payment.User] += payment.Amount
	}
	return balances
}

func computeSettlements(balances map[string]float64) []string {
	type participant struct {
		name string
		bal  float64
	}
	var debtors []participant
	var creditors []participant
	for name, bal := range balances {
		if bal < -0.009 {
			debtors = append(debtors, participant{name, bal})
		} else if bal > 0.009 {
			creditors = append(creditors, participant{name, bal})
		}
	}
	sort.Slice(debtors, func(i, j int) bool { return debtors[i].bal < debtors[j].bal })
	sort.Slice(creditors, func(i, j int) bool { return creditors[i].bal > creditors[j].bal })
	settlements := []string{}
	di := 0
	ci := 0
	for di < len(debtors) && ci < len(creditors) {
		debt := -debtors[di].bal
		credit := creditors[ci].bal
		amount := min(debt, credit)
		settlements = append(settlements, fmt.Sprintf("%s pays %s $%.2f", debtors[di].name, creditors[ci].name, amount))
		debtors[di].bal += amount
		creditors[ci].bal -= amount
		if math.Abs(debtors[di].bal) < 0.01 {
			di++
		}
		if creditors[ci].bal < 0.01 {
			ci++
		}
	}
	return settlements
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type sqliteStore struct {
	db *sql.DB
}

func newSQLiteStore(path string) (*sqliteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	migrated, err := renameLegacyItemParticipants(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureTables(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureItemSettledColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateLegendaryParticipants(db, migrated); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() {
	s.db.Close()
}

func (s *sqliteStore) Items() ([]*Item, error) {
	rows, err := s.db.Query("SELECT id, title, description, cost, settled FROM items ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make(map[int]*Item)
	ordered := make([]int, 0)
	for rows.Next() {
		var id int
		var title, description string
		var cost float64
		var settledInt int
		if err := rows.Scan(&id, &title, &description, &cost, &settledInt); err != nil {
			return nil, err
		}
		items[id] = &Item{
			ID:          id,
			Title:       title,
			Description: description,
			Cost:        cost,
			Settled:     settledInt != 0,
		}
		ordered = append(ordered, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	partRows, err := s.db.Query("SELECT ip.item_id, u.id, u.name FROM item_participants ip JOIN users u ON u.id = ip.user_id ORDER BY ip.item_id, u.name")
	if err != nil {
		return nil, err
	}
	defer partRows.Close()

	for partRows.Next() {
		var itemID int
		var userID int
		var name string
		if err := partRows.Scan(&itemID, &userID, &name); err != nil {
			return nil, err
		}
		if item, ok := items[itemID]; ok {
			item.Participants = append(item.Participants, name)
			item.ParticipantIDs = append(item.ParticipantIDs, userID)
		}
	}
	if err := partRows.Err(); err != nil {
		return nil, err
	}

	result := make([]*Item, 0, len(ordered))
	for _, id := range ordered {
		if item, ok := items[id]; ok {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *sqliteStore) Payments() ([]Payment, error) {
	rows, err := s.db.Query("SELECT item_id, user, amount FROM payments")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	payments := []Payment{}
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ItemID, &p.User, &p.Amount); err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

func (s *sqliteStore) AddItem(title, description string, cost float64, participantIDs []int) (int, error) {
	if title == "" {
		return 0, fmt.Errorf("title is required")
	}
	if len(participantIDs) == 0 {
		return 0, fmt.Errorf("item requires at least one participant")
	}
	participantIDs = uniqueInts(participantIDs)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec("INSERT INTO items (title, description, cost) VALUES (?, ?, ?)", title, description, cost)
	if err != nil {
		return 0, err
	}
	itemID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, userID := range participantIDs {
		if _, err := tx.Exec("INSERT INTO item_participants (item_id, user_id) VALUES (?, ?)", itemID, userID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(itemID), nil
}

func (s *sqliteStore) ItemByID(id int) (*Item, error) {
	if id <= 0 {
		return nil, fmt.Errorf("item not found")
	}
	item := &Item{}
	var settledInt int
	if err := s.db.QueryRow("SELECT id, title, description, cost, settled FROM items WHERE id = ?", id).Scan(&item.ID, &item.Title, &item.Description, &item.Cost, &settledInt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("item not found")
		}
		return nil, err
	}
	item.Settled = settledInt != 0

	partRows, err := s.db.Query("SELECT u.id, u.name FROM item_participants ip JOIN users u ON u.id = ip.user_id WHERE ip.item_id = ? ORDER BY u.name", id)
	if err != nil {
		return nil, err
	}
	defer partRows.Close()

	for partRows.Next() {
		var userID int
		var name string
		if err := partRows.Scan(&userID, &name); err != nil {
			return nil, err
		}
		item.Participants = append(item.Participants, name)
		item.ParticipantIDs = append(item.ParticipantIDs, userID)
	}
	if err := partRows.Err(); err != nil {
		return nil, err
	}

	return item, nil
}

func (s *sqliteStore) UpdateItem(id int, title, description string, cost float64, participantIDs []int, settled bool) error {
	if id <= 0 {
		return fmt.Errorf("invalid item")
	}
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if len(title) > maxTitleLength {
		return fmt.Errorf("title must be at most %d characters", maxTitleLength)
	}
	if len(description) > maxDescriptionLength {
		return fmt.Errorf("description must be at most %d characters", maxDescriptionLength)
	}
	if len(participantIDs) == 0 {
		return fmt.Errorf("item requires at least one participant")
	}
	participantIDs = uniqueInts(participantIDs)
	if err := s.validateUserIDs(participantIDs); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	settledInt := 0
	if settled {
		settledInt = 1
	}
	res, err := tx.Exec("UPDATE items SET title = ?, description = ?, cost = ?, settled = ? WHERE id = ?", title, description, cost, settledInt, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("item not found")
	}

	if _, err := tx.Exec("DELETE FROM item_participants WHERE item_id = ?", id); err != nil {
		return err
	}
	for _, userID := range participantIDs {
		if _, err := tx.Exec("INSERT INTO item_participants (item_id, user_id) VALUES (?, ?)", id, userID); err != nil {
			return err
		}
	}
	if settled {
		if _, err := tx.Exec("DELETE FROM payments WHERE item_id = ?", id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) DeleteItem(id int) error {
	if id <= 0 {
		return fmt.Errorf("invalid item")
	}
	var settled int
	if err := s.db.QueryRow("SELECT settled FROM items WHERE id = ?", id).Scan(&settled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("item not found")
		}
		return err
	}
	if settled == 0 {
		return fmt.Errorf("item must be marked paid up before it can be deleted")
	}
	if _, err := s.db.Exec("DELETE FROM items WHERE id = ?", id); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) AddPayment(itemID int, user string, amount float64) error {
	if user == "" {
		return fmt.Errorf("payer name must be provided")
	}
	if itemID <= 0 {
		return fmt.Errorf("invalid item")
	}
	if amount < minCost {
		return fmt.Errorf("amount must be at least %.2f", minCost)
	}
	if _, err := s.db.Exec("INSERT INTO payments (item_id, user, amount) VALUES (?, ?, ?)", itemID, user, amount); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) Users() ([]User, error) {
	rows, err := s.db.Query("SELECT id, name FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *sqliteStore) AddUser(name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("name must not be empty")
	}
	if len(name) > maxUserNameLength {
		return 0, fmt.Errorf("name must be at most %d characters", maxUserNameLength)
	}
	res, err := s.db.Exec("INSERT INTO users (name) VALUES (?)", name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("user %q already exists", name)
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return int(id), nil
}

func (s *sqliteStore) UpdateUser(id int, name string) error {
	if id <= 0 {
		return fmt.Errorf("invalid user")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > maxUserNameLength {
		return fmt.Errorf("name must be at most %d characters", maxUserNameLength)
	}
	res, err := s.db.Exec("UPDATE users SET name = ? WHERE id = ?", name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("user %q already exists", name)
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *sqliteStore) DeleteUser(id int) error {
	if id <= 0 {
		return fmt.Errorf("invalid user")
	}
	name, err := s.userNameByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return err
	}
	if err := s.userHasReferences(id, name); err != nil {
		return err
	}
	if _, err := s.db.Exec("DELETE FROM users WHERE id = ?", id); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) validateUserIDs(ids []int) error {
	if len(ids) == 0 {
		return fmt.Errorf("select at least one participant")
	}
	stmt := fmt.Sprintf("SELECT id FROM users WHERE id IN (%s)", placeholders(len(ids)))
	rows, err := s.db.Query(stmt, intsToInterface(ids)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[int]struct{}{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		found[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	missing := []int{}
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("unknown participant IDs: %v", missing)
	}
	return nil
}

func (s *sqliteStore) itemExists(id int) (bool, error) {
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM items WHERE id = ? LIMIT 1", id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *sqliteStore) userExistsByName(name string) (bool, error) {
	var id int
	err := s.db.QueryRow("SELECT id FROM users WHERE name = ? LIMIT 1", name).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *sqliteStore) userNameByID(id int) (string, error) {
	var name string
	err := s.db.QueryRow("SELECT name FROM users WHERE id = ? LIMIT 1", id).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (s *sqliteStore) userHasReferences(id int, name string) error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM item_participants WHERE user_id = ?", id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%s still participates in %d item(s) and hasn’t settled the payments, so they cannot be deleted", name, count)
	}
	if name != "" {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM payments WHERE user = ?", name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("%s is referenced in %d payment(s)", name, count)
		}
	}
	return nil
}

func renameLegacyItemParticipants(db *sql.DB) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(item_participants)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	hasName := false
	hasUserID := false
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    interface{}
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		switch name {
		case "user_id":
			hasUserID = true
		case "name":
			hasName = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if !hasName || hasUserID {
		return false, nil
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS item_participants_old"); err != nil {
		return false, err
	}
	if _, err := db.Exec("ALTER TABLE item_participants RENAME TO item_participants_old"); err != nil {
		return false, err
	}
	return true, nil
}

func migrateLegendaryParticipants(db *sql.DB, hasLegacy bool) error {
	if !hasLegacy {
		_, err := db.Exec("DROP TABLE IF EXISTS item_participants_old")
		return err
	}
	rows, err := db.Query("SELECT item_id, name FROM item_participants_old ORDER BY item_id")
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	defer rows.Close()
	type legacy struct {
		itemID int
		name   string
	}
	var entries []legacy
	for rows.Next() {
		var entry legacy
		if err := rows.Scan(&entry.itemID, &entry.name); err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range entries {
		id, err := ensureUser(tx, entry.name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO item_participants (item_id, user_id) VALUES (?, ?)", entry.itemID, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = db.Exec("DROP TABLE IF EXISTS item_participants_old")
	return err
}

type sqlExecQuerier interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func ensureUser(exec sqlExecQuerier, name string) (int, error) {
	var id int
	if err := exec.QueryRow("SELECT id FROM users WHERE name = ?", name).Scan(&id); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := exec.Exec("INSERT INTO users (name) VALUES (?)", name)
	if err != nil {
		return 0, err
	}
	last, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return int(last), nil
}

func ensureTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL UNIQUE
        )`,
		`CREATE TABLE IF NOT EXISTS items (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            description TEXT,
            cost REAL NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS item_participants (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            item_id INTEGER NOT NULL,
            user_id INTEGER NOT NULL,
            UNIQUE (item_id, user_id),
            FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS payments (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            item_id INTEGER NOT NULL,
            user TEXT NOT NULL,
            amount REAL NOT NULL,
            FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
        )`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureItemSettledColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(items)")
	if err != nil {
		return err
	}
	defer rows.Close()

	hasSettled := false
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notnull int
		var dflt interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "settled" {
			hasSettled = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasSettled {
		return nil
	}
	_, err = db.Exec("ALTER TABLE items ADD COLUMN settled INTEGER NOT NULL DEFAULT 0")
	return err
}

func uniqueInts(values []int) []int {
	seen := map[int]struct{}{}
	result := make([]int, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
	}
	return b.String()
}

func intsToInterface(values []int) []interface{} {
	out := make([]interface{}, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func buildDBPath() string {
	if p := os.Getenv("DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("data", "budget.db")
}

func normalizeTab(value string) string {
	if value == usersTabID {
		return usersTabID
	}
	return budgetTabID
}

func redirectToTab(w http.ResponseWriter, r *http.Request, tab string) {
	u := url.URL{Path: "/"}
	if tab != "" {
		q := u.Query()
		q.Set("tab", tab)
		u.RawQuery = q.Encode()
	}
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func redirectWithWarning(w http.ResponseWriter, r *http.Request, msg string, tab string) {
	if msg == "" {
		redirectToTab(w, r, tab)
		return
	}
	u := url.URL{Path: "/"}
	q := u.Query()
	q.Set("userWarning", msg)
	if tab != "" {
		q.Set("tab", tab)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

type itemEditData struct {
	Item      *Item
	Users     []User
	Selected  map[int]bool
	CostValue string
	Warning   string
}

func renderItemEdit(w http.ResponseWriter, tmpl *template.Template, data itemEditData) {
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("item edit template error: %v", err)
	}
}

func renderItemEditPage(w http.ResponseWriter, tmpl *template.Template, item *Item, users []User, selected map[int]bool, costValue, warning string) {
	data := itemEditData{
		Item:      item,
		Users:     users,
		Selected:  selected,
		CostValue: costValue,
		Warning:   warning,
	}
	renderItemEdit(w, tmpl, data)
}

func selectedMap(ids []int) map[int]bool {
	selected := make(map[int]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}
