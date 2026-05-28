package mcoding

import (
	"time"
)

type TransactionProcessor interface {
	Validate() error
	Execute() error
}

type TransferProcessor struct {
	tr          *Transaction
	fromAccount *Account
	toAccount   *Account
	ls          *LedgerService
	ruleEngine  *RuleEngine
}

func NewTransferProcessor(tr *Transaction, ls *LedgerService, fromAccount,
	toAccount *Account) *TransferProcessor {
	tp := &TransferProcessor{
		tr:          tr,
		fromAccount: fromAccount,
		toAccount:   toAccount,
		ls:          ls,
		ruleEngine:  NewRuleEngine(BalanceSufficiencyRule{}, TransactionLimitRule{MaxAmount: 1000000}),
	}
	return tp
}

func (tp *TransferProcessor) Validate() error {
	return tp.ruleEngine.Validate(RuleContext{
		Txn:  tp.tr,
		From: tp.fromAccount,
		To:   tp.toAccount,
	})
}

func (tp *TransferProcessor) Execute() error {
	err := tp.fromAccount.debitUnsafe(tp.tr.amount)
	if err != nil {
		return err
	}

	err = tp.toAccount.creditUnsafe(tp.tr.amount)
	if err != nil {
		if err1 := tp.rollback(); err1 != nil {
			// DEFENSIVE check
			panic("This shouldn't happen")
		}
		return err
	}

	tp.ls.Append(
		LedgerEntry{
			txnId:     tp.tr.txnId,
			accountId: tp.fromAccount.accountId,
			amount:    tp.tr.amount,
			action:    DEBIT,
			createdAt: time.Now(),
		},
		LedgerEntry{
			txnId:     tp.tr.txnId,
			accountId: tp.toAccount.accountId,
			amount:    tp.tr.amount,
			action:    CREDIT,
			createdAt: time.Now(),
		},
	)

	return nil

}

func (tp *TransferProcessor) rollback() error {
	return tp.fromAccount.creditUnsafe(tp.tr.amount)
}

type DepositProcessor struct {
	tr         *Transaction
	toAccount  *Account
	ls         *LedgerService
	ruleEngine *RuleEngine
}

func NewDepositProcessor(tr *Transaction, toAccount *Account, ls *LedgerService) *DepositProcessor {
	return &DepositProcessor{
		tr:         tr,
		toAccount:  toAccount,
		ls:         ls,
		ruleEngine: NewRuleEngine(TransactionLimitRule{MaxAmount: 1000000}),
	}
}

func (dp *DepositProcessor) Validate() error {
	return dp.ruleEngine.Validate(RuleContext{Txn: dp.tr, To: dp.toAccount})
}

func (dp *DepositProcessor) Execute() error {
	if err := dp.toAccount.creditUnsafe(dp.tr.amount); err != nil {
		return err
	}
	dp.ls.Append(LedgerEntry{
		txnId: dp.tr.txnId, accountId: dp.toAccount.accountId,
		amount: dp.tr.amount, action: CREDIT, createdAt: time.Now(),
	})
	return nil
}

type WithdrawalProcessor struct {
	tr          *Transaction
	fromAccount *Account
	ls          *LedgerService
	ruleEngine  *RuleEngine
}

func NewWithdrawalProcessor(tr *Transaction, fromAccount *Account, ls *LedgerService) *WithdrawalProcessor {
	return &WithdrawalProcessor{
		tr:          tr,
		fromAccount: fromAccount,
		ls:          ls,
		ruleEngine:  NewRuleEngine(TransactionLimitRule{MaxAmount: 1000000}),
	}
}

func (wp *WithdrawalProcessor) Validate() error {
	return wp.ruleEngine.Validate(RuleContext{Txn: wp.tr, From: wp.fromAccount})
}

func (wp *WithdrawalProcessor) Execute() error {
	if err := wp.fromAccount.debitUnsafe(wp.tr.amount); err != nil {
		return err
	}
	wp.ls.Append(LedgerEntry{
		txnId: wp.tr.txnId, accountId: wp.fromAccount.accountId,
		amount: wp.tr.amount, action: DEBIT, createdAt: time.Now(),
	})
	return nil
}
