# Budget Divider

Budget Divider is a single-binary Go web app that lets small groups track shared expenses, record who paid what, and compute exactly who owes whom so your broke college friends stay on the same page. The server uses SQLite for persistence, the UI lives in static templates with dedicated CSS/JS files, and the math looks at each item, participants, and payments to generate settlement suggestions.

## Getting started

```bash
git clone https://github.com/mollyoconnorr/BudgetDivider
git checkout main
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

1. Clone the repo, `cd` into it, and make sure you are on `main` (see Getting started).
2. Run `go run .` to build and serve the app on `:8080`.
3. Open `http://localhost:8080/` and use the two tabs:
   - **Budget** – add items, view balances, record payments, and see pending settlements.
   - **Manage users** – create, rename, or delete friends (deletions are blocked if the friend is still tied to items or payments).
4. Stop the server with `Ctrl+C`. Your data stays in `data/budget.db` (or wherever `DB_PATH` points).

## Architecture

- **`main.go`** wires the HTTP server, template functions, and SQLite store factory.
- **`server.go`** defines handlers for `/`, `/item`, `/payment`, `/item/edit`, `/item/update`, `/item/delete`, `/user`, `/user/edit`, and `/user/delete`, and renders templates with balances, settlements, user warnings, and the JSON payload needed by the dashboard JS.
- **`store.go`** handles persistence: it creates/migrates the tables, enforces the `settled` flag, and manages items, participants, payments, and users inside transactions.
- **`helpers.go`** contains validation, formatting, and settlement math helpers; `computeBalances` subtracts per-share amounts from participants and re-adds recorded payments, while `computeSettlements` matches debtors/creditors to produce strings like “Alice pays Bob $12.50.”
- **Templates + static assets** – `templates/index.html` and `templates/item_edit.html` rely on CSS in `static/css` (`main.css`, `edit.css`) and JS in `static/js` (`dashboard.js`, `item_edit.js`). The JSON blob inside `<script type="application/json" id="budget-data">` keeps the dashboard JS decoupled from inline script blocks.

## How the math works (who owes whom)

1. **Per-share debt** – each item divides its cost evenly over its participants (`perShare = cost / len(participants)`). `computeBalances` subtracts that share from every participant’s balance.
2. **Payments** – recorded payments add back to the payer’s balance. If someone paid more than their share, their balance becomes positive; if they still owe, it stays negative.
3. **Settlements** – `computeSettlements` sorts debtors (most negative) and creditors (most positive), then pairs them greedily: the debtor pays the smaller of what they owe or the creditor should receive. The result is strings like “Charlie pays Dana $18.50,” and rounding differences below $0.01 are ignored.

## UI behavior

- Tabs are controlled by the dashboard JavaScript; the server propagates `tab` and `userWarning` query parameters so the correct panel stays visible and warnings stay in context.
- The **Add item** form enforces title/description length limits, requires at least one participant, and disables submission until there are users to select.
- The **Record a payment** form lists only participants of the selected item and caps the `amount` input to that item’s cost. Frontend validation mirrors the server-side checks (minimum $0.10; payments cannot exceed the item's cost).
- Settled items show a bold, color-coded status badge, hide the payment list, and enable the delete control only after marking the item “paid up.”
- Warning overlays (dashboard and edit page) disappear automatically after 10 seconds or when the user taps “Okay.”

## FAQ

**Do I need to create a database after cloning?**
No. Running `go run .` will create `data/budget.db` if it does not exist. The default directory is ignored via `.gitignore`, so deleting the file resets the data while the server is stopped.

**What is the difference between `go.mod` and `go.sum`?**
- `go.mod` describes the module path (`github.com/mollyoconnorr/BudgetDivider`) and dependency requirements.
- `go.sum` records cryptographic checksums for every module version that Go downloads, ensuring future builds fetch the same code.
Both are automatically maintained by `go mod tidy`/`go build`.

**What is `sessions.json`? Should I delete it?**
This project does not generate a `sessions.json`; all persistence happens through SQLite. If you find a `sessions.json` from another experiment, you can delete it—the app never reads it.

**What else can I tweak?**
- Change the default port by editing the `addr` constant in `main.go`.
- Override `DB_PATH` for per-environment SQLite files.
- Extend `store.go` with new columns (e.g., timestamps or currencies) and update the templates accordingly.

## Running tests

Execute:

```bash
go test ./...
```

There are no bundled tests yet, but this command will type-check everything once you add test files.

## References

- [Go wiki tutorial](https://go.dev/doc/articles/wiki/) jumpstarted the web server scaffolding and template rendering.
- ChatGPT helped polish Go syntax and double-check the settlement logic so the math section stays accurate.
