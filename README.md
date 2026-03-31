# Budget Divider 

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

> Running `go run .` automatically downloads dependencies through Go modules.

## Cloning and running

```bash
git clone https://github.com/mollyoconnorr/BudgetDivider
cd BudgetDivider
go run .
# The web UI becomes available at `http://localhost:8080/` after the server starts.
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

1. Adding payments restricts payers to participants of the selected item and caps the `amount` input to that item’s cost; this is enforced both client-side (JavaScript + HTML attributes) and server-side.
2. Warning overlays appear briefly when events fail (e.g., deleting a user with unsettled references); the overlay auto-dismisses after 10 seconds or when the user taps “Okay.”
3. Settled items hide the payment list and instead show “Paid in full” plus a note that all payments were cleared.

Happy budgeting with your broke college friends!

## References
   - [Go wiki](https://go.dev/doc/articles/wiki/) tutorial helped bootstrap the basic web server flow and template rendering.
   - ChatGPT assisted with Go syntax polishing and clarifying the settlement-matching logic so the math section stayed accurate.
