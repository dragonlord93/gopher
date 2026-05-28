package splitwise2

import "sync"

type BalanceService struct {
	userLock map[string]*sync.RWMutex

	// user A -> user B -> context.
	// User A owes user B in which context (direct or group) positive sign mean A owes B, negative mean B owes A
	// Maintained mirror balances suppose A owes B 100 directly then ledger entries
	// ledger[A][B][direct] = 100
	// ledger[B][A][direct] = -100
	// Suppose A owes B 30 in group g1. Then ledger entries are
	// ledger[A][B][group:g1] = 30
	// ledger[B][A][group:g1] = -30
	ledger map[string]map[string]map[string]int64
}

func NewBalanceService() *BalanceService {
	return &BalanceService{
		userLock: make(map[string]*sync.RWMutex),
		ledger:   make(map[string]map[string]map[string]int64),
	}
}

func (bs *BalanceService) Apply(expenseId int, contextId string, debt []DebtEdge) error {
	return nil
}
