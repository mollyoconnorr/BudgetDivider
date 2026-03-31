# Budget Divider (Go)

Budget Divider is a lightweight Go web application that helps friends share and settle group expenses. You can add users, create shared items, record payments, and the app keeps track of who owes whom and which items are fully paid. All data lives in a SQLite database so the information persists between runs.

## Key ideas

- **Users (friends)** are first-class entities. You add a friend, edit their name, and delete them only if they are not tied to any items or payments.
- **Items** represent shared costs (groceries, utilities, a meal, etc.). Each item has a title, optional description, cost, and a set of participants.
- **Payments** record who paid how much toward an item. The UI limits the payer dropdown to the participants for that item, enforces a minimum payment of **$0.10**, and doesn’t allow paying more than the item's total.
- Once an item is marked “paid up” in the edit form, its payment history is cleared and the card shows a “Paid in full” badge; pending items show “Pending payment.”

## Features

1. **Two-tab interface**
   - `Budget` tab lists items, balances, settlements, and lets you create items/payments.
   - `Manage users` tab exists for user creation, inline edits, and deletes (with warnings if the user is still referenced).
2. **Validation**
   - Title ≤ 30 chars, description ≤ 200, names ≤ 50.
   - Cost and payments must be ≥ $0.10 and payments cannot exceed the item cost.
   - Delete actions about users/items surface warnings (now temporary overlays), and you cannot delete a user who still participates in an unsettled item.
3. **SQLite-backed persistence**
   - `data/budget.db` (default) stores users, items, participants, payments, and whether an item has `settled`.
   - Set the `DB_PATH` environment variable before starting to override the default database path.
4. **Settlement math**
   - `computeBalances` subtracts each participant’s share of every item cost from their balance, then adds their recorded payments.
   - `computeSettlements` walks through sorted creditors/debtors and pairs them: the debtor pays the smaller of their owed amount or the creditor’s positive balance, building suggestions like “Alice pays Bob $12.50.” This minimizes the number of transfers.

## Prerequisites

- [Go 1.25.0](https://go.dev/doc/install) (the module declares `go 1.25.0`).
- SQLite3 headers / C toolchain (the app builds `github.com/mattn/go-sqlite3`, which requires CGO).
- Git (for cloning).

> Running `go run .` or `go test ./...` automatically downloads dependencies through Go modules.

## Cloning and running

```bash
git clone <repository-url>
cd Go-WebApp
# (optional) set a custom path for the SQLite file
export DB_PATH="/tmp/budget.db"
# start the web server on :8080
go run .
```

- The application listens on `:8080`. Update the `addr := ":8080"` line in `main.go` if you need a different port, or use a reverse proxy.
- The web UI becomes available at `http://localhost:8080/` after the server starts.
- Stop the server with `Ctrl+C`; the SQLite data stays in `DB_PATH` (default `data/budget.db`).

### Running somewhere else or shipping a binary

```bash
DB_PATH=/var/tmp/budget.db go run .
# or build a standalone binary
go build -o budget-divider .
./budget-divider
```

## Database schema (via `ensureTables`)

| Table | Purpose |
| --- | --- |
| `users` | Friend names (`id`, `name`). Names are unique.
| `items` | Shared expenses with `title`, `description`, `cost`, and `settled` flag.
| `item_participants` | Join table linking `items` to user IDs, enforcing uniqueness and cascading deletes.
| `payments` | Logged payments per item; once an item is marked paid up, its payments are removed.

## How the math works (who owes whom)

1. **Per-share computation**
   - Each item divides its `cost` evenly across its participants: `perShare = cost / len(participants)`.
   - `computeBalances` subtracts `perShare` for every participant (the debt) and then adds each recorded payment.
2. **Balance interpretation**
   - Positive balance → the person has overpaid overall and should receive money back.
   - Negative balance → the person still owes money.
3. **Pairing debtors and creditors**
   - `computeSettlements` sorts debtors (most negative) and creditors (most positive), then iteratively lets the debtor pay the smaller of their debt or the creditor’s credit.
   - It appends human-readable strings such as “Payers name pays Receiver name $X.XX.”
   - Very small remainders (< $0.01) are treated as zero to keep the list tidy.

## UI behaviors worth knowing

1. Tab navigation keeps the selected panel active by capturing the `tab` query parameter in the URL.
2. Adding payments restricts payers to participants of the selected item and caps the `amount` input to that item’s cost; this is enforced both client-side (JavaScript + HTML attributes) and server-side.
3. Warning overlays appear briefly when events fail (e.g., deleting a user with unsettled references); the overlay auto-dismisses after 10 seconds or when the user taps “Okay.”
4. Settled items hide the payment list and instead show “Paid in full” plus a note that all payments were cleared.

## Development Notes

- The server is a single `net/http` binary. Handlers are defined in `main.go` for `/`, `/item`, `/payment`, `/user`, `/item/edit`, etc.
- Templates (`templates/index.html` and `templates/item_edit.html`) use `html/template` to render dynamic data and register helper functions like `perShare`, `formatBalance`, and `formatCurrency` for reuse.
- Validation functions such as `parseCost`, `parseIDs`, and `max length` constants protect the database from bad input.
- SQLite operations often run in transactions (e.g., `AddItem` and `UpdateItem`), ensuring that item/participant updates stay consistent.
- The `sqliteStore` automatically adds the `settled` column when migrating older databases and moves legacy participant tables into the normalized schema.

## Bonus tips

- Manually inspect the data file:
  ```bash
  sqlite3 data/budget.db
  sqlite> .tables
  sqlite> SELECT * FROM users;
  ```
- Reset the application by deleting `data/budget.db` while the server is stopped.
- HTML templates auto-escape user input, which reduces XSS risk.

## Running tests (if added later)

There are no automated tests at the moment, but you can run:

```bash
go test ./...
```

after you add test files or if you want to cover helpers such as `computeBalances`.

## Contribution / Next steps

Some ideas to extend the project:

- Add authentication so multiple households can maintain separate budgets.
- Track receipts or attach notes/images (store as blobs or via a filesystem lookup).
- Offer CSV export/import for offline accounting.
- Schedule regular summary emails or Slack reminders with a cron job.

Happy budgeting with your broke college friends!
