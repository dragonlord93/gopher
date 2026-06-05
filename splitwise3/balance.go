package splitwise3

import (
	"sort"
	"sync"
)

type BalanceService struct {
	mu       sync.Mutex // guards userLock map + applied set + top-level ledger keys
	userLock map[string]*sync.RWMutex
	applied  map[string]struct{} // dedup by mutationKey

	// ledger[from][to][ctx] = paise `from` owes `to` in that context.
	// Mirrored: ledger[A][B][ctx] == -ledger[B][A][ctx].
	ledger map[string]map[string]map[string]int64
}

func NewBalanceService() *BalanceService {
	return &BalanceService{
		userLock: make(map[string]*sync.RWMutex),
		applied:  make(map[string]struct{}),
		ledger:   make(map[string]map[string]map[string]int64),
	}
}

// ensure lazily creates the user's lock AND ledger row under mu, so the row
// pointer is stable and safe to mutate once we hold the user's write lock.
func (bs *BalanceService) ensure(user string) *sync.RWMutex {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	l, ok := bs.userLock[user]
	if !ok {
		l = &sync.RWMutex{}
		bs.userLock[user] = l
	}
	if bs.ledger[user] == nil {
		bs.ledger[user] = map[string]map[string]int64{}
	}
	return l
}

func (bs *BalanceService) Apply(mutationKey, contextId string, edges []DebtEdge) error {
	// Distinct users touched, sorted => deadlock-free lock acquisition.
	set := map[string]struct{}{}
	for _, e := range edges {
		set[e.creditor] = struct{}{}
		set[e.debitor] = struct{}{}
	}
	users := make([]string, 0, len(set))
	for u := range set {
		users = append(users, u)
	}
	sort.Strings(users)

	locks := make([]*sync.RWMutex, 0, len(users))
	for _, u := range users {
		l := bs.ensure(u)
		l.Lock()
		locks = append(locks, l)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}()

	// Idempotency. Same key touches the same users, so any concurrent duplicate
	// is already blocked on the locks above -> only one application can win.
	bs.mu.Lock()
	if _, done := bs.applied[mutationKey]; done {
		bs.mu.Unlock()
		return nil
	}
	bs.mu.Unlock()

	for _, e := range edges {
		if e.amount == 0 {
			continue
		}
		bs.add(e.debitor, e.creditor, contextId, e.amount)  // debitor owes creditor
		bs.add(e.creditor, e.debitor, contextId, -e.amount) // mirror
	}

	bs.mu.Lock()
	bs.applied[mutationKey] = struct{}{}
	bs.mu.Unlock()
	return nil
}

// caller must hold `from`'s write lock; ledger[from] guaranteed non-nil by ensure().
func (bs *BalanceService) add(from, to, ctx string, amt int64) {
	row := bs.ledger[from]
	if row[to] == nil {
		row[to] = map[string]int64{}
	}
	row[to][ctx] += amt
}

// Balance within one context, from `user`'s perspective.
// positive => user owes that person; negative => that person owes user.
func (bs *BalanceService) Balance(user, contextId string) map[string]int64 {
	l := bs.ensure(user)
	l.RLock()
	defer l.RUnlock()
	out := map[string]int64{}
	for other, byCtx := range bs.ledger[user] {
		if v := byCtx[contextId]; v != 0 {
			out[other] = v
		}
	}
	return out
}

// Net balance across ALL contexts (the home-screen view).
func (bs *BalanceService) OverallBalance(user string) map[string]int64 {
	l := bs.ensure(user)
	l.RLock()
	defer l.RUnlock()
	out := map[string]int64{}
	for other, byCtx := range bs.ledger[user] {
		var sum int64
		for _, v := range byCtx {
			sum += v
		}
		if sum != 0 {
			out[other] = sum
		}
	}
	return out
}
