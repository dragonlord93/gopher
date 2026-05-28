package mcoding

import (
	"fmt"
	"sync"
	"time"
)

type LedgerAction int

const (
	CREDIT LedgerAction = iota
	DEBIT
)

func (l LedgerAction) String() string {
	switch l {
	case CREDIT:
		return "Credit"
	case DEBIT:
		return "Debit"
	default:
		return "Unknown"
	}

}

type LedgerEntry struct {
	txnId     string
	amount    Money
	action    LedgerAction
	accountId string
	createdAt time.Time
}

type Ledger struct {
	sync.RWMutex
	entries []LedgerEntry
}

type LedgerService struct {
	l *Ledger
}

func NewLedgerService() *LedgerService {
	return &LedgerService{
		l: &Ledger{
			RWMutex: sync.RWMutex{},
			entries: []LedgerEntry{},
		},
	}
}

func (ls *LedgerService) Append(entries ...LedgerEntry) {
	ls.l.Lock()
	defer ls.l.Unlock()
	ls.l.entries = append(ls.l.entries, entries...)
}

func (ls *LedgerService) Print() {
	ls.l.RLock()
	defer ls.l.RUnlock()
	for _, entry := range ls.l.entries {
		fmt.Printf("txn Id=%s, amount=%d, currency=%s, createdAt=%v, action=%s", entry.txnId, entry.amount.amount,
			entry.amount.currency, entry.createdAt, entry.action)
	}
}
