/**
 * server.go wires the HTTP handlers, templates, and redirect helpers together,
 * exposing the Budget Divider routes that manage items, payments, and users.
 */
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

/**
 * itemEditData feeds the item edit template with the current item, selected
 * participants, and validation warnings emitted during updates.
 */
// itemEditData feeds the item edit template with the current item, selected
// participants, and any validation warnings.
type itemEditData struct {
	Item      *Item
	Users     []User
	Selected  map[int]bool
	CostValue string
	Warning   string
}

/**
 * registerHandlers installs every Budget Divider endpoint on the provided mux
 * so incoming HTTP requests are routed to the correct handler logic.
 */
func registerHandlers(mux *http.ServeMux, store *sqliteStore, tmpl *template.Template, editTmpl *template.Template) {
	// Budget and user endpoints use the same mux so we can extend easily.
	mux.HandleFunc("/", indexHandler(store, tmpl))
	mux.HandleFunc("/item", itemHandler(store))
	mux.HandleFunc("/payment", paymentHandler(store))
	mux.HandleFunc("/item/edit", itemEditHandler(store, editTmpl))
	mux.HandleFunc("/item/update", itemUpdateHandler(store, editTmpl))
	mux.HandleFunc("/item/delete", itemDeleteHandler(store, editTmpl))
	mux.HandleFunc("/user", userHandler(store))
	mux.HandleFunc("/user/edit", userEditHandler(store))
	mux.HandleFunc("/user/delete", userDeleteHandler(store))
}

/**
 * indexHandler renders the main budget dashboard, supplying items, payments,
 * computed balances, settlement suggestions, and any user-visible warnings.
 */
func indexHandler(store *sqliteStore, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		// Build helper structures for the JS that filters payers and limits costs.
		// Build helper structures for the JS that filters payers and limits costs.
		participantMap := make(map[int][]string, len(items))
		itemCosts := make(map[int]float64, len(items))
		for _, item := range items {
			itemCosts[item.ID] = item.Cost
			if len(item.Participants) == 0 {
				continue
			}
			participantMap[item.ID] = append([]string(nil), item.Participants...)
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
		appData := template.JS(fmt.Sprintf("{\"participants\": %s, \"itemCosts\": %s}", participantJSON, costJSON))

		paymentsByItem := map[int][]Payment{}
		for _, p := range payments {
			// Group payments so the template can list them per item.
			paymentsByItem[p.ItemID] = append(paymentsByItem[p.ItemID], p)
		}

		balances := computeBalances(items, payments)
		settlements := computeSettlements(balances)
		activeTab := normalizeTab(r.URL.Query().Get("tab"))
		warning := r.URL.Query().Get("userWarning")

		renderTemplate(w, tmpl, items, paymentsByItem, balances, settlements, users, warning, activeTab, appData)
	}
}

/**
 * itemHandler validates the new item payload (title, cost, participants) and
 * persists it along with participant assignments in a transactional fashion.
 */
func itemHandler(store *sqliteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not read form", http.StatusBadRequest)
			return
		}
		// Trim whitespace before validation so users cannot sneak spaces.
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
	}
}

/**
 * paymentHandler records a payment amount and payer for a specific item,
 * validating that the amount does not exceed the item cost and that a payer
 * is selected.
 */
func paymentHandler(store *sqliteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

/**
 * itemEditHandler serves the edit-item page for the requested ID so users can
 * adjust details before resubmitting.
 */
func itemEditHandler(store *sqliteStore, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		renderItemEditPage(w, tmpl, item, users, selectedMap(item.ParticipantIDs), fmt.Sprintf("%.2f", item.Cost), "")
	}
}

/**
 * itemUpdateHandler accepts edits from the edit form, validates every field,
 * updates the item participants, and optionally toggles the settled flag.
 */
func itemUpdateHandler(store *sqliteStore, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		inputItem := &Item{ID: id, Title: title, Description: description, ParticipantIDs: participantIDs, Settled: paidUp}

		if title == "" {
			renderItemEditPage(w, tmpl, inputItem, users, selected, rawCost, "title is required")
			return
		}
		if len(title) > maxTitleLength {
			renderItemEditPage(w, tmpl, inputItem, users, selected, rawCost, fmt.Sprintf("title must be at most %d characters", maxTitleLength))
			return
		}
		if len(description) > maxDescriptionLength {
			renderItemEditPage(w, tmpl, inputItem, users, selected, rawCost, fmt.Sprintf("description must be at most %d characters", maxDescriptionLength))
			return
		}
		if costErr != nil {
			renderItemEditPage(w, tmpl, inputItem, users, selected, rawCost, costErr.Error())
			return
		}
		inputItem.Cost = cost
		if err := store.UpdateItem(id, title, description, cost, participantIDs, paidUp); err != nil {
			renderItemEditPage(w, tmpl, inputItem, users, selected, fmt.Sprintf("%.2f", cost), err.Error())
			return
		}
		redirectToTab(w, r, budgetTabID)
	}
}

/**
 * itemDeleteHandler deletes a shared expense only once it has been marked
 * settled so historical references are cleaned up safely.
 */
func itemDeleteHandler(store *sqliteStore, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			renderItemEditPage(w, tmpl, item, users, selectedMap(item.ParticipantIDs), fmt.Sprintf("%.2f", item.Cost), "mark the item as paid up before deleting")
			return
		}
		if err := store.DeleteItem(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		redirectToTab(w, r, budgetTabID)
	}
}

/**
 * userHandler validates and creates a new user entry, ensuring the name is
 * neither empty nor too long before persisting.
 */
func userHandler(store *sqliteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

/**
 * userEditHandler renames a friend while keeping uniqueness constraints intact.
 */
func userEditHandler(store *sqliteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

/**
 * userDeleteHandler deletes users only after verifying they do not participate
 * in unsettled items or past payments.
 */
func userDeleteHandler(store *sqliteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

/**
 * renderTemplate assembles the dashboard view model and executes the
 * template with pre-computed balances, settlements, participants, and costs.
 */
func renderTemplate(w http.ResponseWriter, tmpl *template.Template, items []*Item, payments map[int][]Payment, balances map[string]float64, settlements []string, users []User, warning string, activeTab string, appData template.JS) {
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
		Settlements []string
		Users       []User
		UserWarning string
		ActiveTab   string
		AppData     template.JS
	}{
		Items:       items,
		Payments:    payments,
		Balances:    balances,
		BalanceList: balanceList,
		Settlements: settlements,
		Users:       users,
		UserWarning: warning,
		ActiveTab:   activeTab,
		AppData:     appData,
	}
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template error: %v", err)
	}
}

/**
 * selectedMap converts participant IDs into a map for quick template lookups.
 */
func selectedMap(ids []int) map[int]bool {
	selected := make(map[int]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}

/**
 * renderItemEdit executes the item edit template and logs any rendering
 * errors.
 */
func renderItemEdit(w http.ResponseWriter, tmpl *template.Template, data itemEditData) {
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("item edit template error: %v", err)
	}
}

/**
 * renderItemEditPage packages the item, user list, selected participants, and
 * warning message before rendering the edit view.
 */
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

/**
 * redirectToTab navigates back to a specific tab (Budget or Manage Users)
 * by encoding the tab name in the redirect target.
 */
func redirectToTab(w http.ResponseWriter, r *http.Request, tab string) {
	u := url.URL{Path: "/"}
	if tab != "" {
		q := u.Query()
		q.Set("tab", tab)
		u.RawQuery = q.Encode()
	}
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

/**
 * redirectWithWarning attaches a warning message and tab parameter to the
 * redirect so the UI can surface failures without losing context.
 */
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
