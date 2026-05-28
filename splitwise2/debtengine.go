package splitwise2

type DebtEdge struct {
	creditor string
	debitor  string
	amount   int64
}

type DebtEngine interface {
	DeriveEdges(paidBy map[string]int64, splits *Splits) ([]DebtEdge, error)
}

type ProportionalDebtEngine struct{}

func (p *ProportionalDebtEngine) DeriveEdges(
	paidBy map[string]int64, splits *Splits,
) ([]DebtEdge, error) {
	// Compute net position per user: paid - owed
	// Net creditors (paid > owed) receive from net debtors (owed > paid)
	// Distribute each debtor's shortfall proportionally across creditors' surpluses
	// Return []DebtEdge
	return nil, nil
}
