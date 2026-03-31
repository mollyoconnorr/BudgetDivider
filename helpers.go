/**
 * helpers.go bundles shared validation, formatting, and tab-state utilities for
 * the Budget Divider UI.
 */
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

/**
 * parseIDs converts checkbox values to ints, ignoring blanks and malformed input.
 */
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

/**
 * parseCost validates that the cost string is present, numeric, and satisfies
 * the minimum allowed value before returning it.
 */
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

/**
 * perShare returns the per-participant share amount for an item.
 */
func perShare(item *Item) float64 {
	if item == nil || len(item.Participants) == 0 {
		return 0
	}
	return item.Cost / float64(len(item.Participants))
}

/**
 * formatBalance renders balances with explicit +/− signs for clarity in the UI.
 */
func formatBalance(balance float64) string {
	if balance >= 0 {
		return fmt.Sprintf("+$%.2f", balance)
	}
	return fmt.Sprintf("-$%.2f", math.Abs(balance))
}

/**
 * formatCurrency formats a float64 as USD with two decimal places.
 */
func formatCurrency(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

/**
 * computeBalances subtracts each participant's share from their balance and
 * re-adds recorded payments to compute the net owed/owed-to amount.
 */
func computeBalances(items []*Item, payments []Payment) map[string]float64 {
	balances := map[string]float64{}
	for _, item := range items {
		if len(item.Participants) == 0 {
			continue
		}
		// Subtract each participant's share so the balance reflects what they owe.
		share := item.Cost / float64(len(item.Participants))
		for _, p := range item.Participants {
			balances[p] -= share
		}
	}
	for _, payment := range payments {
		// Reapply earlier payments to update net balances.
		balances[payment.User] += payment.Amount
	}
	return balances
}

/**
 * computeSettlements turns balances into debt/credit pairs and produces text
 * like "A pays B $12.50" for the dashboard.
 * ChatGPT helped with the logic of this function
 */
func computeSettlements(balances map[string]float64) []string {
	type participant struct {
		name string
		bal  float64
	}
	var debtors, creditors []participant
	for name, bal := range balances {
		if bal < -0.009 {
			// Negative balances represent people who still owe money.
			debtors = append(debtors, participant{name, bal})
		} else if bal > 0.009 {
			// Positive balances are credits owed back to them.
			creditors = append(creditors, participant{name, bal})
		}
	}
	// Sort debtors/creditors so we can greedily pair the largest offsets first.
	sort.Slice(debtors, func(i, j int) bool { return debtors[i].bal < debtors[j].bal })
	sort.Slice(creditors, func(i, j int) bool { return creditors[i].bal > creditors[j].bal })

	settlements := []string{}
	di, ci := 0, 0
	for di < len(debtors) && ci < len(creditors) {
		debt := -debtors[di].bal
		credit := creditors[ci].bal
		amount := min(debt, credit)
		settlements = append(settlements, fmt.Sprintf("%s pays %s $%.2f", debtors[di].name, creditors[ci].name, amount))
		debtors[di].bal += amount
		creditors[ci].bal -= amount
		// Advance whichever side is effectively settled (allowing for rounding).
		if math.Abs(debtors[di].bal) < 0.01 {
			di++
		}
		if creditors[ci].bal < 0.01 {
			ci++
		}
	}
	return settlements
}

/**
 * min returns the smaller of two floats.
 */
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

/**
 * buildDBPath chooses the SQLite file location, respecting the DB_PATH env var.
 */
func buildDBPath() string {
	if p := os.Getenv("DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("data", "budget.db")
}

/**
 * normalizeTab defaults any unknown tab value back to the budget view.
 */
func normalizeTab(value string) string {
	if value == usersTabID {
		return usersTabID
	}
	return budgetTabID
}
