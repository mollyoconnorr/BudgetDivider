# Budget Divider

Budget Divider is a single-binary Go web app that lets small groups track shared expenses, record who paid what, and compute exactly who owes whom so your broke college friends stay on the same page. The server uses SQLite for persistence, the UI lives in static templates with dedicated CSS/JS files, and the math looks at each item, participants, and payments to generate settlement suggestions.

## Getting started

```bash
git clone https://github.com/mollyoconnorr/BudgetDivider
cd BudgetDivider
# (optional) change where the SQLite file lives
export DB_PATH="/tmp/budget.db"
# run the server on http://localhost:8080
go run .
```

Once the server is running, visit `http://localhost:8080/` to open the dashboard. The first request will create `data/budget.db` automatically (unless you override `DB_PATH`), so you do not need to manually create the database; the directory is ignored by git.

## Prerequisites

- **Go 1.25.0+** – install via [https://go.dev/doc/install](https://go.dev/doc/install).
- **CGO toolchain** – `mattn/go-sqlite3` requires C headers, so make sure you have a compiler (Xcode command line tools on macOS, build-essential on Linux).
- **Git** – to clone the repository (and keep history tidy).

## Running the app

1. Clone the repo and `cd` into it (see above).
2. Run `go run .` to build and serve the app on `:8080`.
3. Open the URL in your browser and use the two tabs:
   - **Budget** – add items, view balances, record payments, and see pending settlements.
   - **Manage users** – create, rename, or delete friends (deletions are blocked if the friend is still tied to items/payments).
4. Stop the server with `Ctrl+C`. Your data stays in `data/budget.db` (unless you changed `DB_PATH`).

## Architecture

- **`main.go`** wires the HTTP server, template functions, and SQLite store factory.
- **`server.go`** defines handlers for `/`, `/item`, `/payment`, `/item/edit`, `/item/update`, `/item/delete`, `/user`, `/user/edit`, and `/user/delete`, and it renders the templates with helper data (balances, settlements, user warning, and JSON needed by the dashboard JS).
- **`store.go`** is the persistence layer: it creates tables, enforces the `settled` flag, and manages items, users, payments, and participant links inside transactions.
- **`helpers.go`** holds validation, formatting, and settlement math; `computeBalances` subtracts per-share amounts from each participant and re-adds their payments, while `computeSettlements` walks balances to produce human-readable strings like “Alice pays Bob $12.50.”
- **Templates + static assets** – `templates/index.html` and `templates/item_edit.html` rely on dedicated CSS files under `static/css` and JS files under `static/js` (`dashboard.js` for the main tab logic and `item_edit.js` for the edit form). The JSON blob injected into `<script type="application/json" id="budget-data">` keeps the dashboard JavaScript decoupled from inline `<script>` tags.

## How the math works (who owes whom)

1. **Per-share debt** – each item divides its cost evenly over its participants (`perShare = cost / len(participants)`). `computeBalances` subtracts that share from every participant’s balance.
2. **Payments** – recorded payments add back to the payer’s balance. If someone paid more than their share, their balance becomes positive; if they still owe, it stays negative.
3. **Settlements** – `computeSettlements` sorts debtors (most negative) and creditors (most positive), then pairs them greedily: the debtor pays the smaller of what they owe or what the creditor should receive. The result is a short list of strings like “Charlie pays Dana $18.50,” and tiny rounding differences (<$0.01) are ignored.

## UI behavior

- The two tabs are fully controlled by the dashboard JavaScript; the server embeds `tab` and `userWarning` query parameters so the right panel stays visible and overlay warnings show where intended.
- The **Add item** form enforces title/description length limits, requires at least one participant, and blocks submission until there are users to pick from.
- The **Record a payment** form only lists payers that belong to the selected item and caps the amount input to that item’s total. Frontend validation mirrors the server-side checks (minimum $0.10; cannot exceed the item cost).
- Settled items display a bold, color-coded status badge, hide the payment list, and unlock the delete button only after marking the item “paid up.”
- Warning overlays (both dashboard and edit page) disappear automatically after 10 seconds or when the user taps “Okay.”

## FAQ

**Do I need to create a database after cloning?**
No. Running `go run .` will create `data/budget.db` if it does not exist. The default directory is ignored via `.gitignore`, so you can safely delete `data/budget.db` to reset the app.

**What is the difference between `go.mod` and `go.sum`?**
- `go.mod` describes the module path (`github.com/mollyoconnorr/BudgetDivider`) and the dependency requirements.
- `go.sum` records cryptographic checksums for every module version that Go downloads, ensuring future builds grab exactly the same code.
Both are maintained automatically by `go mod tidy`/`go build`.

**What is `sessions.json`? Should I delete it?**
This project does not generate a `sessions.json`; all persistence happens through SQLite. If you find a `sessions.json` file from another experiment, you can delete it or keep it elsewhere – nothing in this repo reads it.

**What else can I tweak?**
- Change the default port by editing the `addr` constant in `main.go`.
- Override `DB_PATH` for per-environment SQLite files (e.g., `/tmp/budget.db`).
- Extend `store.go` with new columns (e.g., timestamps or currencies) and update the templates accordingly.

## Running tests

Execute:

```bash
go test ./...
```

There are no tests bundled yet, but this command will type-check everything once you add test files.

## References

- [Go wiki tutorial](https://go.dev/doc/articles/wiki/) jumpstarted the web server scaffolding and template rendering.
- ChatGPT helped polish Go syntax and double-check the settlement logic so the math section stays accurate.
