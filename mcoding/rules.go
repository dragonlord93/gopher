package mcoding

import "fmt"

type Rule interface {
	Validate(ctx RuleContext) error
}

type RuleContext struct {
	Txn  *Transaction
	From *Account // nil for DEPOSIT
	To   *Account // nil for WITHDRAWAL
}

type RuleEngine struct {
	rules []Rule
}

func NewRuleEngine(rules ...Rule) *RuleEngine {
	return &RuleEngine{rules: rules}
}

func (e *RuleEngine) AddRule(r Rule) {
	e.rules = append(e.rules, r)
}

func (e *RuleEngine) Validate(ctx RuleContext) error {
	for _, rule := range e.rules {
		if err := rule.Validate(ctx); err != nil {
			return err // fail fast on first error
		}
	}
	return nil
}

type BalanceSufficiencyRule struct{}

func (r BalanceSufficiencyRule) Validate(ctx RuleContext) error {
	if ctx.From == nil {
		return nil // not applicable (e.g., DEPOSIT)
	}
	if ctx.From.balance.amount < ctx.Txn.amount.amount {
		return fmt.Errorf("insufficient balance: account=%s required=%d available=%d",
			ctx.From.accountId, ctx.Txn.amount.amount, ctx.From.balance.amount)
	}
	return nil
}

type TransactionLimitRule struct {
	MaxAmount int
}

func (r TransactionLimitRule) Validate(ctx RuleContext) error {
	if ctx.Txn.amount.amount > r.MaxAmount {
		return fmt.Errorf("transaction exceeds limit: amount=%d max=%d",
			ctx.Txn.amount.amount, r.MaxAmount)
	}
	return nil
}
