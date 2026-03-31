# Budget Divider

Budget Divider is a single-binary Go web app that lets small groups track shared expenses, record who paid what, and compute exactly who owes whom so your broke college friends stay on the same page. The server uses SQLite for persistence, the UI lives in static templates with dedicated CSS/JS files, and the math looks at each item, participants, and payments to generate settlement suggestions.

## Getting started

```bash
git clone https://github.com/mollyoconnorr/BudgetDivider
cd BudgetDivider
go run .
# run the server on http://localhost:8080
```

## Prerequisites

- **Go 1.25.0+** – install via [https://go.dev/doc/install](https://go.dev/doc/install).

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

## References

- [Go wiki tutorial](https://go.dev/doc/articles/wiki/) helped jumpstart the web server and template rendering.
- ChatGPT helped with various Go syntax and double-check the settlement logic so the math section was accurate.
