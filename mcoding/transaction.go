package mcoding

import (
	"fmt"
	"sync"
	"time"
)

type TransactionStatus int

const (
	PENDING TransactionStatus = iota
	SUCCESS
	FAILED
)

func (s TransactionStatus) String() string {
	switch s {
	case PENDING:
		return "PENDING"
	case SUCCESS:
		return "SUCCESS"
	case FAILED:
		return "FAILED"
	default:
		return "Unknown"
	}
}

type TransactionType int

const (
	DEPOSIT TransactionType = iota
	WITHDRAWAL
	TRANSFER
)

var transactationState = map[TransactionStatus]map[TransactionStatus]struct{}{
	PENDING: {
		SUCCESS: {},
		FAILED:  {},
	},
}

type Transaction struct {
	txnId           string
	amount          Money
	fromAccount     string
	toAccount       string
	createdAt       time.Time
	updatedAt       time.Time
	status          TransactionStatus
	transactionType TransactionType
}

type TrasnsactionService struct {
	sync.RWMutex
	transactions map[string]*Transaction
	ls           *LedgerService
	as           *AccountService
}

func NewTransactionService(ls *LedgerService, as *AccountService) *TrasnsactionService {

	ts := &TrasnsactionService{
		RWMutex:      sync.RWMutex{},
		transactions: map[string]*Transaction{},
		ls:           ls,
		as:           as,
	}
	return ts
}

func (ts *TrasnsactionService) Transfer(txnId, fromAccount, toAccount string, amount Money) (*Transaction, error) {
	if err := ts.validateTxnRequest(txnId, fromAccount, toAccount, amount, TRANSFER); err != nil {
		return nil, err
	}
	ts.Lock()
	if existing, ok := ts.transactions[txnId]; ok {
		ts.Unlock()
		return dedupeReturn(existing)
	}
	tr := &Transaction{
		txnId:           txnId,
		fromAccount:     fromAccount,
		toAccount:       toAccount,
		amount:          amount,
		status:          PENDING,
		createdAt:       time.Now(),
		transactionType: TRANSFER,
	}
	ts.transactions[txnId] = tr
	ts.Unlock()

	err := ts.as.WithAccountsLocked([]string{fromAccount, toAccount}, func(accs map[string]*Account) error {
		fromAcc := accs[fromAccount]
		toAcc := accs[toAccount]
		processor := NewTransferProcessor(tr, ts.ls, fromAcc, toAcc)
		return ts.Process(processor, txnId)
	})
	if err != nil {
		if err1 := ts.MarkFailed(txnId); err1 != nil {
			panic("This should not happen even, marking the transaction from pending to failed")
		}
		return nil, err
	}

	if err := ts.MarkSuccess(txnId); err != nil {
		panic("This should not happen even, marking the transaction from pending to success")
	}

	return tr, nil
}

func (ts *TrasnsactionService) Deposit(txnId, toAccount string, amount Money) (*Transaction, error) {
	if err := ts.validateTxnRequest(txnId, "", toAccount, amount, DEPOSIT); err != nil {
		return nil, err
	}
	ts.Lock()
	if existing, ok := ts.transactions[txnId]; ok {
		ts.Unlock()
		return dedupeReturn(existing)
	}
	tr := &Transaction{
		txnId:           txnId,
		toAccount:       toAccount,
		amount:          amount,
		status:          PENDING,
		createdAt:       time.Now(),
		transactionType: DEPOSIT,
	}
	ts.transactions[txnId] = tr
	ts.Unlock()

	err := ts.as.WithAccountsLocked([]string{toAccount}, func(accs map[string]*Account) error {
		toAccObj := accs[toAccount]
		processor := NewDepositProcessor(tr, toAccObj, ts.ls)
		return ts.Process(processor, txnId)
	})
	if err != nil {
		if err1 := ts.MarkFailed(txnId); err1 != nil {
			panic("This should not happen even, marking the transaction from pending to failed")
		}
		return nil, err
	}

	// Move to SUCCESS state
	if err := ts.MarkSuccess(txnId); err != nil {
		panic("This should not happen even, marking the transaction from pending to success")
	}

	return tr, nil
}

func (ts *TrasnsactionService) validateTxnRequest(txnId, fromAccount, toAccount string, amount Money, transType TransactionType) error {
	if txnId == "" {
		return fmt.Errorf("txnId cannot be empty")
	}
	if amount.amount <= 0 {
		return fmt.Errorf("amount cannot be 0 or less")
	}
	switch transType {
	case TRANSFER:
		fromAccountObj, err := ts.as.Get(fromAccount)
		if err != nil {
			return err
		}
		toAccountObj, err := ts.as.Get(toAccount)
		if err != nil {
			return err
		}
		if fromAccountObj.balance.currency != amount.currency || toAccountObj.balance.currency != amount.currency {
			return fmt.Errorf("currency mismatch")
		}
	case WITHDRAWAL:
		fromAccountObj, err := ts.as.Get(fromAccount)
		if err != nil {
			return err
		}
		if fromAccountObj.balance.currency != amount.currency {
			return fmt.Errorf("currency mismatch")
		}
	case DEPOSIT:
		toAccountObj, err := ts.as.Get(fromAccount)
		if err != nil {
			return err
		}
		if toAccountObj.balance.currency != amount.currency {
			return fmt.Errorf("currency mismatch")
		}
	default:
		return fmt.Errorf("unknown transaction type")
	}
	return nil
}

func (ts *TrasnsactionService) Process(processor TransactionProcessor, txnId string) error {
	err := processor.Validate()
	if err != nil {
		return err
	}
	err = processor.Execute()
	if err != nil {
		return err
	}
	return nil
}

func dedupeReturn(existing *Transaction) (*Transaction, error) {
	switch existing.status {
	case SUCCESS:
		return existing, nil
	case PENDING:
		return existing, ErrTxnInFlight
	case FAILED:
		return existing, ErrTxnFailed
	default:
		return existing, fmt.Errorf("unknown status: %s", existing.status)
	}
}

func (ts *TrasnsactionService) updateTxnStatus(txnId string, status TransactionStatus) error {
	ts.Lock()
	defer ts.Unlock()
	if _, ok := ts.transactions[txnId]; !ok {
		return fmt.Errorf("transaction doesn't exist, txnId=%s", txnId)
	}
	currentStatus := ts.transactions[txnId].status
	finalState := status
	if _, ok := transactationState[currentStatus]; !ok {
		return fmt.Errorf("cannot mark failed transaction status from %s", currentStatus)
	}
	if _, ok := transactationState[currentStatus][finalState]; !ok {
		return fmt.Errorf("cannot mark failed transaction status from %s", currentStatus)
	}

	ts.transactions[txnId].status = finalState
	ts.transactions[txnId].updatedAt = time.Now()
	return nil
}

func (ts *TrasnsactionService) MarkFailed(txnId string) error {
	return ts.updateTxnStatus(txnId, FAILED)
}

func (ts *TrasnsactionService) MarkSuccess(txnId string) error {
	return ts.updateTxnStatus(txnId, SUCCESS)
}
