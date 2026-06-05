package splitwise3

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// =====================================================================
// EqualSplitProcessor
// =====================================================================

func TestEqualSplit_EvenDivision(t *testing.T) {
	p := NewEqualSplitProcessor()
	s, err := p.Split(300, map[string]int64{"A": 0, "B": 0, "C": 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"A", "B", "C"} {
		if s.splits[u] != 100 {
			t.Errorf("%s: got %d, want 100", u, s.splits[u])
		}
	}
}

func TestEqualSplit_RemainderGoesToFirstSortedUser(t *testing.T) {
	// 100 / 3 = 33 r 1. lex-sorted first user ("alice") gets the extra paisa.
	p := NewEqualSplitProcessor()
	s, _ := p.Split(100, map[string]int64{"alice": 0, "bob": 0, "carol": 0})

	if s.splits["alice"] != 34 {
		t.Errorf("alice: got %d, want 34", s.splits["alice"])
	}
	if s.splits["bob"] != 33 {
		t.Errorf("bob: got %d, want 33", s.splits["bob"])
	}
	if s.splits["carol"] != 33 {
		t.Errorf("carol: got %d, want 33", s.splits["carol"])
	}

	// Conservation: shares must sum exactly to total.
	var sum int64
	for _, v := range s.splits {
		sum += v
	}
	if sum != 100 {
		t.Errorf("shares sum to %d, want 100", sum)
	}
}

func TestEqualSplit_EmptyParticipants(t *testing.T) {
	p := NewEqualSplitProcessor()
	if _, err := p.Split(100, nil); err == nil {
		t.Error("expected error for empty participants")
	}
}

// =====================================================================
// ProportionalDebtEngine
// =====================================================================

func TestProportional_SinglePayer(t *testing.T) {
	d := &ProportionalDebtEngine{}
	splits := &Splits{splits: map[string]int64{"A": 100, "B": 100, "C": 100}}
	edges, err := d.DeriveEdges(map[string]int64{"A": 300}, splits)
	if err != nil {
		t.Fatal(err)
	}
	assertEdges(t, edges, []DebtEdge{
		{creditor: "A", debitor: "B", amount: 100},
		{creditor: "A", debitor: "C", amount: 100},
	})
}

func TestProportional_TwoCreditors(t *testing.T) {
	// A paid 180, B paid 120, all owe 100. Surpluses A:80 B:20. C owes 100 split 80/20.
	d := &ProportionalDebtEngine{}
	splits := &Splits{splits: map[string]int64{"A": 100, "B": 100, "C": 100}}
	edges, _ := d.DeriveEdges(map[string]int64{"A": 180, "B": 120}, splits)
	assertEdges(t, edges, []DebtEdge{
		{creditor: "A", debitor: "C", amount: 80},
		{creditor: "B", debitor: "C", amount: 20},
	})
}

func TestProportional_NoDebt(t *testing.T) {
	// Everyone paid exactly their share => no edges.
	d := &ProportionalDebtEngine{}
	splits := &Splits{splits: map[string]int64{"A": 100, "B": 100}}
	edges, _ := d.DeriveEdges(map[string]int64{"A": 100, "B": 100}, splits)
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestProportional_RoundingConservesPerDebtor(t *testing.T) {
	// Forces remainder allocation. A=70 B=30, all 4 owe 25.
	// Surplus A:45 B:5, total 50. C owes 25: A=22.5→22, B=2.5→2, leftover 1.
	d := &ProportionalDebtEngine{}
	splits := &Splits{splits: map[string]int64{"A": 25, "B": 25, "C": 25, "D": 25}}
	edges, _ := d.DeriveEdges(map[string]int64{"A": 70, "B": 30}, splits)

	debtorTotals := map[string]int64{}
	for _, e := range edges {
		debtorTotals[e.debitor] += e.amount
	}
	if debtorTotals["C"] != 25 {
		t.Errorf("C edges sum %d, want exactly 25 (rounding lost paise)", debtorTotals["C"])
	}
	if debtorTotals["D"] != 25 {
		t.Errorf("D edges sum %d, want exactly 25", debtorTotals["D"])
	}
}

// =====================================================================
// SimplifyingDebtEngine
// =====================================================================

func TestSimplifying_PreservesNetPositions(t *testing.T) {
	// Same input as proportional test above; different attribution, same net per user.
	d := &SimplifyingDebtEngine{}
	paidBy := map[string]int64{"A": 250, "B": 150}
	splits := &Splits{splits: map[string]int64{"A": 100, "B": 100, "C": 100, "D": 100}}
	edges, _ := d.DeriveEdges(paidBy, splits)

	nets := map[string]int64{}
	for _, e := range edges {
		nets[e.creditor] += e.amount
		nets[e.debitor] -= e.amount
	}
	want := map[string]int64{"A": 150, "B": 50, "C": -100, "D": -100}
	for u, v := range want {
		if nets[u] != v {
			t.Errorf("user %s net from edges %d, want %d", u, nets[u], v)
		}
	}
}

func TestSimplifying_EdgeCountBound(t *testing.T) {
	// At most |creditors| + |debtors| - 1 edges.
	d := &SimplifyingDebtEngine{}
	paidBy := map[string]int64{"A": 250, "B": 150}
	splits := &Splits{splits: map[string]int64{"A": 100, "B": 100, "C": 100, "D": 100}}
	edges, _ := d.DeriveEdges(paidBy, splits)
	if len(edges) > 3 { // 2 creditors + 2 debtors - 1
		t.Errorf("got %d edges, expected ≤ 3", len(edges))
	}
}

// =====================================================================
// BalanceService
// =====================================================================

func TestBalance_MirroredLedger(t *testing.T) {
	bs := NewBalanceService()
	bs.Apply("m1", "direct", []DebtEdge{{creditor: "A", debitor: "B", amount: 100}})

	if v := bs.Balance("B", "direct")["A"]; v != 100 {
		t.Errorf("B's view of A: got %d, want +100", v)
	}
	if v := bs.Balance("A", "direct")["B"]; v != -100 {
		t.Errorf("A's view of B: got %d, want -100 (mirror)", v)
	}
}

func TestBalance_IdempotentOnSameMutationKey(t *testing.T) {
	bs := NewBalanceService()
	edges := []DebtEdge{{creditor: "A", debitor: "B", amount: 100}}
	bs.Apply("m1", "direct", edges)
	bs.Apply("m1", "direct", edges)
	bs.Apply("m1", "direct", edges)
	if v := bs.Balance("B", "direct")["A"]; v != 100 {
		t.Errorf("after 3 same-key applies, got %d (want 100, no double-apply)", v)
	}
}

func TestBalance_DifferentKeysCompose(t *testing.T) {
	bs := NewBalanceService()
	edges := []DebtEdge{{creditor: "A", debitor: "B", amount: 100}}
	bs.Apply("m1", "direct", edges)
	bs.Apply("m2", "direct", edges) // different key => applies again
	if v := bs.Balance("B", "direct")["A"]; v != 200 {
		t.Errorf("two distinct mutations: got %d, want 200", v)
	}
}

func TestBalance_ReversalCancelsOriginal(t *testing.T) {
	bs := NewBalanceService()
	fwd := []DebtEdge{{creditor: "A", debitor: "B", amount: 100}}
	rev := []DebtEdge{{creditor: "B", debitor: "A", amount: 100}} // swap = negate

	bs.Apply("m1:fwd", "direct", fwd)
	bs.Apply("m1:rev", "direct", rev)

	if v := bs.Balance("B", "direct")["A"]; v != 0 {
		t.Errorf("after reversal, B->A: got %d, want 0", v)
	}
	if v := bs.Balance("A", "direct")["B"]; v != 0 {
		t.Errorf("mirror after reversal, A->B: got %d, want 0", v)
	}
}

func TestBalance_ContextsAreSeparate(t *testing.T) {
	bs := NewBalanceService()
	bs.Apply("m1", "direct", []DebtEdge{{creditor: "A", debitor: "B", amount: 100}})
	bs.Apply("m2", "group:g1", []DebtEdge{{creditor: "A", debitor: "B", amount: 50}})

	if v := bs.Balance("B", "direct")["A"]; v != 100 {
		t.Errorf("direct: got %d, want 100", v)
	}
	if v := bs.Balance("B", "group:g1")["A"]; v != 50 {
		t.Errorf("group: got %d, want 50", v)
	}
	if v := bs.OverallBalance("B")["A"]; v != 150 {
		t.Errorf("overall (sum across contexts): got %d, want 150", v)
	}
}

// Run with `go test -race` to catch concurrent-access bugs in the ledger.
func TestBalance_ConcurrentApplies(t *testing.T) {
	bs := NewBalanceService()
	var wg sync.WaitGroup
	n := 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bs.Apply(fmt.Sprintf("m%d", i), "direct",
				[]DebtEdge{{creditor: "A", debitor: "B", amount: 1}})
		}(i)
	}
	wg.Wait()
	if v := bs.Balance("B", "direct")["A"]; v != int64(n) {
		t.Errorf("after %d concurrent applies, got %d, want %d", n, v, n)
	}
}

// =====================================================================
// ExpenseService
// =====================================================================

func TestExpenseService_AddIsIdempotent(t *testing.T) {
	es := NewExpenseService()
	e1, _ := es.AddExpense(&Expense{idempotencyKey: "K1", totalAmount: 100})
	e2, _ := es.AddExpense(&Expense{idempotencyKey: "K1", totalAmount: 999})
	if e1 != e2 {
		t.Error("retry returned different expense pointer")
	}
	if e2.totalAmount != 100 {
		t.Errorf("retry's payload leaked into existing expense: %d", e2.totalAmount)
	}
}

func TestExpenseService_MarkAppliedStateMachine(t *testing.T) {
	es := NewExpenseService()
	e, _ := es.AddExpense(&Expense{idempotencyKey: "K1"})
	if e.status != Pending {
		t.Errorf("new expense status: %v, want Pending", e.status)
	}
	if err := es.MarkApplied(e.id); err != nil {
		t.Fatal(err)
	}
	if e.status != Applied {
		t.Error("after MarkApplied: not Applied")
	}
	// Idempotent: second call must not error and not change state.
	if err := es.MarkApplied(e.id); err != nil {
		t.Errorf("second MarkApplied errored: %v", err)
	}
}

// =====================================================================
// Orchestrator — end-to-end
// =====================================================================

func newOrchestrator() *Orchestrator {
	return NewOrchestrator(NewExpenseService(), &ProportionalDebtEngine{}, NewBalanceService())
}

func TestOrchestrator_AddExpenseAppliesBalances(t *testing.T) {
	o := newOrchestrator()
	e, err := o.AddExpense("K1", "direct",
		map[string]int64{"A": 300},
		map[string]int64{"A": 0, "B": 0, "C": 0},
		300)
	if err != nil {
		t.Fatal(err)
	}
	if e.status != Applied {
		t.Errorf("status: %v, want Applied", e.status)
	}
	if v := o.bs.Balance("B", "direct")["A"]; v != 100 {
		t.Errorf("B owes A: %d, want 100", v)
	}
	if v := o.bs.Balance("C", "direct")["A"]; v != 100 {
		t.Errorf("C owes A: %d, want 100", v)
	}
}

// This is the Case D regression test from the idempotency discussion:
// retrying AddExpense with the same key must NOT double-apply balances.
func TestOrchestrator_AddExpenseRetryDoesNotDoubleApply(t *testing.T) {
	o := newOrchestrator()
	paidBy := map[string]int64{"A": 300}
	sharedBy := map[string]int64{"A": 0, "B": 0, "C": 0}

	e1, _ := o.AddExpense("K1", "direct", paidBy, sharedBy, 300)
	e2, _ := o.AddExpense("K1", "direct", paidBy, sharedBy, 300) // retry
	e3, _ := o.AddExpense("K1", "direct", paidBy, sharedBy, 300) // retry again

	if e1.id != e2.id || e2.id != e3.id {
		t.Errorf("retries returned different expense ids: %d, %d, %d", e1.id, e2.id, e3.id)
	}
	if v := o.bs.Balance("B", "direct")["A"]; v != 100 {
		t.Errorf("after retries, B owes A: %d, want 100", v)
	}
}

func TestOrchestrator_EditExpenseReversesOldAppliesNew(t *testing.T) {
	o := newOrchestrator()
	e, _ := o.AddExpense("K1", "direct",
		map[string]int64{"A": 300},
		map[string]int64{"A": 0, "B": 0, "C": 0},
		300)

	// Edit: A and B now each pay half. Shared participants unchanged.
	_, err := o.EditExpense("E1", e.id,
		map[string]int64{"A": 150, "B": 150},
		map[string]int64{"A": 0, "B": 0, "C": 0},
		300)
	if err != nil {
		t.Fatal(err)
	}

	// Post-edit nets: A surplus 50, B surplus 50, C debt 100.
	// Proportional: C owes A 50, C owes B 50; B owes A 0.
	if v := o.bs.Balance("B", "direct")["A"]; v != 0 {
		t.Errorf("B owes A after edit: %d, want 0", v)
	}
	if v := o.bs.Balance("C", "direct")["A"]; v != 50 {
		t.Errorf("C owes A after edit: %d, want 50", v)
	}
	if v := o.bs.Balance("C", "direct")["B"]; v != 50 {
		t.Errorf("C owes B after edit: %d, want 50", v)
	}
}

func TestOrchestrator_EditExpenseRetryIsIdempotent(t *testing.T) {
	o := newOrchestrator()
	e, _ := o.AddExpense("K1", "direct",
		map[string]int64{"A": 300},
		map[string]int64{"A": 0, "B": 0, "C": 0},
		300)

	newPaid := map[string]int64{"A": 150, "B": 150}
	shared := map[string]int64{"A": 0, "B": 0, "C": 0}

	// Three identical edit calls; balances must reflect ONE edit, not three.
	for i := 0; i < 3; i++ {
		if _, err := o.EditExpense("E1", e.id, newPaid, shared, 300); err != nil {
			t.Fatal(err)
		}
	}

	if v := o.bs.Balance("C", "direct")["A"]; v != 50 {
		t.Errorf("after edit retries, C owes A: %d, want 50", v)
	}
	if v := o.bs.Balance("C", "direct")["B"]; v != 50 {
		t.Errorf("after edit retries, C owes B: %d, want 50", v)
	}
}

// =====================================================================
// Test helpers
// =====================================================================

func assertEdges(t *testing.T, got, want []DebtEdge) {
	t.Helper()
	g, w := edgeSet(got), edgeSet(want)
	if !equalSlices(g, w) {
		t.Errorf("edges mismatch:\n  got:  %s\n  want: %s",
			strings.Join(g, ", "), strings.Join(w, ", "))
	}
}

func edgeSet(edges []DebtEdge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = fmt.Sprintf("%s->%s:%d", e.debitor, e.creditor, e.amount)
	}
	sort.Strings(out)
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
