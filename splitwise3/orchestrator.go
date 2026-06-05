package splitwise3

import "fmt"

type Orchestrator struct {
	es *ExpenseService
	de DebtEngine
	bs *BalanceService
}

func NewOrchestrator(es *ExpenseService, de DebtEngine, bs *BalanceService) *Orchestrator {
	return &Orchestrator{
		es: es,
		de: de,
		bs: bs,
	}
}

func (o *Orchestrator) AddExpense(idempotencyKey, contextId string,
	paidBy, sharedBy map[string]int64, totalAmount int64) (*Expense, error) {

	e := &Expense{
		idempotencyKey: idempotencyKey,
		contextId:      contextId,
		paidBy:         paidBy,
		sharedBy:       sharedBy,
		totalAmount:    totalAmount,
		status:         Pending,
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}

	splits, err := o.splitter(e.splitType).Split(totalAmount, sharedBy)
	if err != nil {
		return nil, err
	}
	edges, err := o.de.DeriveEdges(paidBy, splits)
	if err != nil {
		return nil, err
	}
	e.edges = edges // persist edges WITH the expense (crash-recovery + reversal)

	e, err = o.es.AddExpense(e) // returns existing on retry; e.edges is that one's
	if err != nil {
		return e, err
	}

	if e.status != Pending {
		return e, nil
	}

	mutKey := fmt.Sprintf("%d:0:fwd", e.id)
	if err := o.bs.Apply(mutKey, e.contextId, e.edges); err != nil {
		return e, err // stays Pending; a reconciler can replay this same key safely
	}
	if err := o.es.MarkApplied(e.id); err != nil {
		return e, err
	}
	return e, nil
}

func (o *Orchestrator) EditExpense(idempotencyKey string, expenseId int,
	newPaidBy, newSharedBy map[string]int64, newTotalAmount int64) (*Expense, error) {

	e, ok := o.es.Get(expenseId)
	if !ok {
		return nil, fmt.Errorf("expense not found: %d", expenseId)
	}

	// Validate the new payment side sums to the new total.
	if err := (&Expense{paidBy: newPaidBy, totalAmount: newTotalAmount}).Validate(); err != nil {
		return e, err
	}

	splits, err := o.splitter(e.splitType).Split(newTotalAmount, newSharedBy)
	if err != nil {
		return e, err
	}
	newEdges, err := o.de.DeriveEdges(newPaidBy, splits)
	if err != nil {
		return e, err
	}

	rec, err := o.es.BeginEdit(idempotencyKey, expenseId, newEdges, newPaidBy, newSharedBy, newTotalAmount)
	if err != nil {
		return e, err
	}

	// Reverse the old edges, then apply the new — two independently-keyed,
	// independently-idempotent mutations.
	revKey := fmt.Sprintf("%d:%d:rev", expenseId, rec.revision)
	if err := o.bs.Apply(revKey, e.contextId, negate(rec.oldEdges)); err != nil {
		return e, err
	}
	fwdKey := fmt.Sprintf("%d:%d:fwd", expenseId, rec.revision)
	if err := o.bs.Apply(fwdKey, e.contextId, rec.newEdges); err != nil {
		return e, err
	}

	if err := o.es.CommitEdit(idempotencyKey); err != nil {
		return e, err
	}
	ex, _ := o.es.Get(expenseId)
	return ex, nil
}

// negate produces the inverse of each edge by swapping creditor/debitor.
// Applying negate(edges) exactly undoes Apply(edges) in the mirrored ledger.
func negate(edges []DebtEdge) []DebtEdge {
	out := make([]DebtEdge, len(edges))
	for i, e := range edges {
		out[i] = DebtEdge{creditor: e.debitor, debitor: e.creditor, amount: e.amount}
	}
	return out
}

func (o *Orchestrator) splitter(splitType SplitType) ExpenseSplitProcessor {
	switch splitType {
	case EQUALLY:
		return NewEqualSplitProcessor()
	}
	return nil
}
