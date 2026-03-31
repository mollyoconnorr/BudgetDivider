/**
 * Budget Divider is a lightweight Go web server that tracks shared expenses,
 * records payments, and calculates clear settlements between friends. The
 * application persists data in SQLite, validates inputs, and exposes a
 * two-tab interface (budget + user management) via HTML templates.
 */
package main

import (
	"html/template"
	"log"
	"net/http"
	"path/filepath"
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

/**
 * main builds and runs the web server that drives the Budget Divider UI.
 */
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
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	registerHandlers(mux, store, tmpl, editTmpl)

	addr := ":8080"
	log.Printf("server listening %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
