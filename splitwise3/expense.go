package splitwise3 // --- Expense: store applied edges + revision so edits can reverse-then-reapply ---

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type SplitType int

const (
	EQUALLY SplitType = iota
	PERCENTAGE
)

type Splits struct {
	splits map[string]int64
}

type ExpenseSplitProcessor interface {
	Split(totalAmount int64, sharedBy map[string]int64) (*Splits, error)
}

type EqualSplitProcessor struct {
}

func NewEqualSplitProcessor() ExpenseSplitProcessor {
	return &EqualSplitProcessor{}
}

func (ep *EqualSplitProcessor) Split(totalAmount int64, sharedBy map[string]int64) (*Splits, error) {
	n := int64(len(sharedBy))
	if n == 0 {
		return nil, fmt.Errorf("no participants")
	}
	base := totalAmount / n
	remainder := totalAmount - base*n // leftover paise from integer division

	// Deterministic remainder distribution: sort userIds, give the first
	// `remainder` users one extra paisa each. This guarantees sum == total
	// and is reproducible across runs (important for testing & audit).
	ids := make([]string, 0, n)
	for id := range sharedBy {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := &Splits{
		splits: map[string]int64{},
	}
	for i, id := range ids {
		out.splits[id] = base
		if int64(i) < remainder {
			amt := out.splits[id]
			amt += 1
			out.splits[id] = amt
		}
	}
	return out, nil
}

type User struct {
	id        string
	name      string
	createdAt time.Time
	updatedAt time.Time
}

type ExpenseStatus int

const (
	Pending ExpenseStatus = iota
	Applied
	Reversed
)

type Expense struct {
	id             int
	paidBy         map[string]int64
	sharedBy       map[string]int64
	createdAt      time.Time
	updatedAt      time.Time
	splitType      SplitType
	totalAmount    int64
	idempotencyKey string
	status         ExpenseStatus
	contextId      string

	revision int        // bumped per successful edit; feeds the mutation key
	edges    []DebtEdge // currently-applied edges (what an edit must reverse)
}

func (e *Expense) Validate() error {
	paidTotal := int64(0)
	for _, amt := range e.paidBy {
		paidTotal += amt
	}
	if paidTotal != e.totalAmount {
		return fmt.Errorf("Total amount paid doesn't match by the total users paid")
	}
	return nil
}

func (es *ExpenseService) Get(id int) (*Expense, bool) {
	es.mu.RLock()
	defer es.mu.RUnlock()
	e, ok := es.expenses[id]
	return e, ok
}

// --- editRecord: makes EditExpense idempotent at the service layer ---
type editRecord struct {
	expenseId   int
	revision    int
	oldEdges    []DebtEdge
	newEdges    []DebtEdge
	newPaidBy   map[string]int64
	newSharedBy map[string]int64
	newTotal    int64
	committed   bool
}

type ExpenseService struct {
	mu             *sync.RWMutex
	idCntr         int
	expenses       map[int]*Expense
	expensesByIdem map[string]*Expense
	edits          map[string]*editRecord // edit idempotencyKey -> record
}

func NewExpenseService() *ExpenseService {
	return &ExpenseService{
		mu:             &sync.RWMutex{},
		expenses:       map[int]*Expense{},
		expensesByIdem: map[string]*Expense{},
		edits:          map[string]*editRecord{},
		idCntr:         0,
	}
}

func (es *ExpenseService) AddExpense(e *Expense) (*Expense, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if existing, ok := es.expensesByIdem[e.idempotencyKey]; ok {
		return existing, nil // idempotent
	}
	e.createdAt = time.Now()
	es.idCntr++
	e.id = es.idCntr
	es.expenses[e.id] = e
	es.expensesByIdem[e.idempotencyKey] = e
	return e, nil
}

func (es *ExpenseService) MarkApplied(id int) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	e, ok := es.expenses[id]
	if !ok {
		return fmt.Errorf("expense not found: %d", id)
	}
	switch e.status {
	case Pending:
		e.status = Applied
		e.updatedAt = time.Now()
	case Applied:
		// idempotent no-op
	case Reversed:
		return fmt.Errorf("expense %d is reversed", id)
	}
	return nil
}

// BeginEdit reserves a revision for this edit. Idempotent on idemKey: a retry
// returns the SAME record, so the orchestrator replays identical mutation keys
// (which the balance service then dedupes). Does NOT mutate the expense yet.
func (es *ExpenseService) BeginEdit(idemKey string, expenseId int, newEdges []DebtEdge,
	newPaidBy, newSharedBy map[string]int64, newTotal int64) (*editRecord, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if r, ok := es.edits[idemKey]; ok {
		return r, nil
	}
	e, ok := es.expenses[expenseId]
	if !ok {
		return nil, fmt.Errorf("expense not found: %d", expenseId)
	}
	if e.status == Reversed {
		return nil, fmt.Errorf("cannot edit reversed expense %d", expenseId)
	}
	r := &editRecord{
		expenseId:   expenseId,
		revision:    e.revision + 1,
		oldEdges:    e.edges,
		newEdges:    newEdges,
		newPaidBy:   newPaidBy,
		newSharedBy: newSharedBy,
		newTotal:    newTotal,
	}
	es.edits[idemKey] = r
	return r, nil
}

// CommitEdit flips the head state to the new revision once both balance
// mutations have landed. Idempotent.
func (es *ExpenseService) CommitEdit(idemKey string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	r, ok := es.edits[idemKey]
	if !ok {
		return fmt.Errorf("unknown edit: %s", idemKey)
	}
	if r.committed {
		return nil
	}
	e := es.expenses[r.expenseId]
	e.paidBy = r.newPaidBy
	e.sharedBy = r.newSharedBy
	e.totalAmount = r.newTotal
	e.edges = r.newEdges
	e.revision = r.revision
	e.status = Applied
	e.updatedAt = time.Now()
	r.committed = true
	return nil
}
