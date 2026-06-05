package splitwise3

import (
	"fmt"
	"sort"
)

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
	if splits == nil {
		return nil, fmt.Errorf("nil splits")
	}

	// net[u] = paid - owed.  >0 => creditor (others owe them), <0 => debtor.
	net := map[string]int64{}
	for u, paid := range paidBy {
		net[u] += paid
	}
	for u, owed := range splits.splits {
		net[u] -= owed
	}

	type pos struct {
		user   string
		amount int64
	}
	var creditors, debtors []pos
	var totalSurplus int64
	for u, n := range net {
		switch {
		case n > 0:
			creditors = append(creditors, pos{u, n})
			totalSurplus += n
		case n < 0:
			debtors = append(debtors, pos{u, -n}) // store shortfall as positive
		}
	}

	// Everyone paid exactly their share: nothing owed.
	if totalSurplus == 0 {
		return []DebtEdge{}, nil
	}

	// Deterministic order => reproducible edges (matters for tests & audit).
	sort.Slice(creditors, func(i, j int) bool { return creditors[i].user < creditors[j].user })
	sort.Slice(debtors, func(i, j int) bool { return debtors[i].user < debtors[j].user })

	var edges []DebtEdge
	for _, d := range debtors {
		// Allocate d.amount across creditors proportional to surplus.
		// floor each cell, then hand out leftover paise by largest remainder
		// so the row sums EXACTLY to d.amount.
		type alloc struct {
			creditor string
			floor    int64
			rem      int64
		}
		allocs := make([]alloc, 0, len(creditors))
		var assigned int64
		for _, c := range creditors {
			num := d.amount * c.amount // NB: overflow if amounts are absurdly large
			fl := num / totalSurplus
			allocs = append(allocs, alloc{c.user, fl, num % totalSurplus})
			assigned += fl
		}
		leftover := d.amount - assigned // always in [0, len(creditors))

		sort.Slice(allocs, func(i, j int) bool {
			if allocs[i].rem != allocs[j].rem {
				return allocs[i].rem > allocs[j].rem
			}
			return allocs[i].creditor < allocs[j].creditor // stable tie-break
		})
		for k := 0; int64(k) < leftover; k++ {
			allocs[k].floor++
		}

		for _, a := range allocs {
			if a.floor > 0 {
				edges = append(edges, DebtEdge{creditor: a.creditor, debitor: d.user, amount: a.floor})
			}
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].debitor != edges[j].debitor {
			return edges[i].debitor < edges[j].debitor
		}
		return edges[i].creditor < edges[j].creditor
	})
	return edges, nil
}

type SimplifyingDebtEngine struct{}

func (s *SimplifyingDebtEngine) DeriveEdges(
	paidBy map[string]int64, splits *Splits,
) ([]DebtEdge, error) {
	if splits == nil {
		return nil, fmt.Errorf("nil splits")
	}

	net := map[string]int64{}
	for u, paid := range paidBy {
		net[u] += paid
	}
	for u, owed := range splits.splits {
		net[u] -= owed
	}

	type pos struct {
		user   string
		amount int64
	}
	var creditors, debtors []pos
	for u, n := range net {
		if n > 0 {
			creditors = append(creditors, pos{u, n})
		} else if n < 0 {
			debtors = append(debtors, pos{u, -n}) // shortfall as positive
		}
	}

	// Largest first, so big imbalances get knocked out early.
	sort.Slice(creditors, func(i, j int) bool { return creditors[i].amount > creditors[j].amount })
	sort.Slice(debtors, func(i, j int) bool { return debtors[i].amount > debtors[j].amount })

	var edges []DebtEdge
	i, j := 0, 0
	for i < len(debtors) && j < len(creditors) {
		settle := min64(debtors[i].amount, creditors[j].amount)
		edges = append(edges, DebtEdge{
			creditor: creditors[j].user,
			debitor:  debtors[i].user,
			amount:   settle,
		})
		debtors[i].amount -= settle
		creditors[j].amount -= settle
		if debtors[i].amount == 0 {
			i++
		}
		if creditors[j].amount == 0 {
			j++
		}
	}
	return edges, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
