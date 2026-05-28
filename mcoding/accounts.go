package mcoding

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

type Account struct {
	sync.RWMutex
	accountId string
	name      string
	balance   Money
	createdAt time.Time
	updatedAt time.Time
}

type AccountService struct {
	sync.RWMutex
	account map[string]*Account
}

func NewAccountService() *AccountService {
	return &AccountService{
		RWMutex: sync.RWMutex{},
		account: map[string]*Account{},
	}
}

func (as *AccountService) Get(accountId string) (*Account, error) {
	as.RLock()
	defer as.RUnlock()
	if _, ok := as.account[accountId]; !ok {
		return nil, fmt.Errorf("Account doesn't exist")
	}
	acc := as.account[accountId]
	return acc, nil
}

func (as *AccountService) WithAccountsLocked(accountIds []string, fn func(accounts map[string]*Account) error) error {
	// Dedupe + sort to prevent deadlock
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(accountIds))
	for _, id := range accountIds {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	slices.Sort(unique)

	// Fetch all accounts upfront
	accounts := make(map[string]*Account, len(unique))
	as.RLock()
	for _, id := range unique {
		acc, ok := as.account[id]
		if !ok {
			as.RUnlock()
			return fmt.Errorf("accountId=%s, %w", id, ErrAccountNotFound)
		}
		accounts[id] = acc
	}
	as.RUnlock()

	// Acquire in sorted order
	for _, id := range unique {
		accounts[id].Lock()
	}
	defer func() {
		// Release in reverse order (convention, not strictly necessary)
		for i := len(unique) - 1; i >= 0; i-- {
			accounts[unique[i]].Unlock()
		}
	}()

	return fn(accounts)
}

func (a *Account) debitUnsafe(amount Money) error {
	newBalance, err := a.balance.Subtract(amount)
	if err != nil {
		return err
	}
	a.balance = newBalance
	return nil
}

func (a *Account) creditUnsafe(amount Money) error {
	newBalance, err := a.balance.Add(amount)
	if err != nil {
		return err
	}
	a.balance = newBalance
	return nil
}
