package splitwise2

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

type ExpenseService struct {
	mu             *sync.RWMutex
	idCntr         int
	expenses       map[int]*Expense
	expensesByIdem map[string]*Expense
}

func NewExpenseService() *ExpenseService {
	return &ExpenseService{
		mu:             &sync.RWMutex{},
		expenses:       map[int]*Expense{},
		expensesByIdem: map[string]*Expense{},
		idCntr:         0,
	}
}

func (es *ExpenseService) AddExpense(e *Expense) (*Expense, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if e, ok := es.expensesByIdem[e.idempotencyKey]; ok {
		return e, nil
	}
	e.createdAt = time.Now()
	es.idCntr++
	e.id = es.idCntr
	es.expenses[e.id] = e
	es.expensesByIdem[e.idempotencyKey] = e
	return e, nil
}

func (es *ExpenseService) MarkApplied(e *Expense) {
	// Unsafe currently its not thread safe
	e.status = Applied
}

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

func (o *Orchestrator) AddExpense(idempotencyKey, contextId string, paidBy map[string]int64, sharedBy map[string]int64,
	totalAmount int64) (*Expense, error) {
	e := &Expense{
		idempotencyKey: idempotencyKey,
		paidBy:         paidBy,
		sharedBy:       sharedBy,
		totalAmount:    totalAmount,
		status:         Pending,
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	e, err := o.es.AddExpense(e)
	if err != nil {
		return nil, err
	}
	splits, err := o.splitter(e.splitType).Split(totalAmount, sharedBy)
	if err != nil {
		return nil, err
	}
	debtEdges, err := o.de.DeriveEdges(paidBy, splits)
	if err != nil {
		return nil, err
	}
	if err := o.bs.Apply(e.id, e.contextId, debtEdges); err != nil {
		return nil, err
	}
	o.es.MarkApplied(e)
	return e, nil
}

func (o *Orchestrator) splitter(splitType SplitType) ExpenseSplitProcessor {
	switch splitType {
	case EQUALLY:
		return NewEqualSplitProcessor()
	}
	return nil
}
