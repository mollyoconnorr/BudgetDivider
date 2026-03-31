/**
 * store.go contains the SQLite persistence layer, migrations, and helper
 * utilities for creating, updating, and deleting items, payments, and users.
 */
package main

import (
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"path/filepath"
	"strings"
)

/**
 * sqliteStore wraps the SQLite connection and exposes helper methods for
 * managing items, participants, payments, and users.
 */
type sqliteStore struct {
	db *sql.DB
}

/**
 * newSQLiteStore opens or creates the SQLite database, enables foreign keys,
 * and runs migrations to ensure necessary tables/columns exist.
 */
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

// Close closes the underlying SQLite connection.
func (s *sqliteStore) Close() {
	s.db.Close()
}

/**
 * Items loads every shared expense along with its participant names/IDs.
 */
func (s *sqliteStore) Items() ([]*Item, error) {
	rows, err := s.db.Query("SELECT id, title, description, cost, settled FROM items ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make(map[int]*Item)
	ordered := make([]int, 0)
	// Read every item and keep track of the query order for deterministic output.
	for rows.Next() {
		var id, settledInt int
		var title, description string
		var cost float64
		if err := rows.Scan(&id, &title, &description, &cost, &settledInt); err != nil {
			return nil, err
		}
		items[id] = &Item{ID: id, Title: title, Description: description, Cost: cost, Settled: settledInt != 0}
		ordered = append(ordered, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch every participant once and stitch them into the relevant item.
	partRows, err := s.db.Query("SELECT ip.item_id, u.id, u.name FROM item_participants ip JOIN users u ON u.id = ip.user_id ORDER BY ip.item_id, u.name")
	if err != nil {
		return nil, err
	}
	defer partRows.Close()

	for partRows.Next() {
		var itemID, userID int
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

/**
 * Payments returns every payment stored in the database.
 */
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

/**
 * AddItem inserts a shared expense and its participants within a transaction,
 * ensuring both the item and item_participants rows remain in sync.
 */
func (s *sqliteStore) AddItem(title, description string, cost float64, participantIDs []int) (int, error) {
	if title == "" {
		return 0, fmt.Errorf("title is required")
	}
	if len(participantIDs) == 0 {
		return 0, fmt.Errorf("item requires at least one participant")
	}
	participantIDs = uniqueInts(participantIDs)

	// Use a transaction so items and their participant links stay consistent.
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
		// Link each participant to the newly created item.
		if _, err := tx.Exec("INSERT INTO item_participants (item_id, user_id) VALUES (?, ?)", itemID, userID); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(itemID), nil
}

/**
 * ItemByID fetches an item and its linked participants for editing.
 */
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

/**
 * UpdateItem modifies an item and resets its participant relationships. If
 * the item is marked settled, existing payments are deleted.
 */
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
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("item not found")
	}

	// Refresh the participant list before re-inserting.
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

/**
 * DeleteItem removes an item only after it is marked settled to avoid losing
 * in-flight payments.
 */
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

/**
 * AddPayment inserts a payment row tied to the payer and item.
 */
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

/**
 * Users lists all friends alphabetically.
 */
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

/**
 * AddUser inserts a new friend, enforcing uniqueness/length.
 */
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

/**
 * UpdateUser renames a friend while keeping uniqueness constraints intact.
 */
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
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

/**
 * DeleteUser removes a friend when they have zero references to items/payments.
 */
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

/**
 * validateUserIDs ensures each participant ID exists in the users table.
 */
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

/**
 * itemExists reports whether an item with the provided ID exists.
 */
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

/**
 * userExistsByName checks whether a friend already exists by name.
 */
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

/**
 * userNameByID returns a friend's name for a given ID.
 */
func (s *sqliteStore) userNameByID(id int) (string, error) {
	var name string
	err := s.db.QueryRow("SELECT name FROM users WHERE id = ? LIMIT 1", id).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

/**
 * userHasReferences checks whether the friend appears in item_participants
 * or payments.
 */
func (s *sqliteStore) userHasReferences(id int, name string) error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM item_participants WHERE user_id = ?", id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%s still participates in %d item(s) and hasn't settled the payments, so they cannot be deleted", name, count)
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

/**
 * renameLegacyItemParticipants renames an old participant table when
 * legacy columns are detected.
 */
func renameLegacyItemParticipants(db *sql.DB) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(item_participants)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	hasName := false
	hasUserID := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt interface{}
		var pk int
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

/**
 * migrateLegendaryParticipants migrates legacy participant rows into the
 * current schema.
 */
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

/**
 * ensureTables creates the necessary tables if they are missing.
 */
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
		// Create or re-use every required table.
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

/**
 * ensureItemSettledColumn adds the settled column to items when needed.
 */
func ensureItemSettledColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(items)")
	if err != nil {
		return err
	}
	defer rows.Close()

	hasSettled := false
	for rows.Next() {
		var cid int
		var name, typ string
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
	// If column already exists, no migration needed.
	if hasSettled {
		return nil
	}
	_, err = db.Exec("ALTER TABLE items ADD COLUMN settled INTEGER NOT NULL DEFAULT 0")
	return err
}

/**
 * uniqueInts removes duplicate ints while preserving the original order.
 */
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

/**
 * placeholders builds a "?,?" placeholder list for IN clauses.
 */
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

/**
 * intsToInterface converts an int slice into []interface{} for queries.
 */
func intsToInterface(values []int) []interface{} {
	out := make([]interface{}, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
