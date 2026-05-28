package mcoding

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func setup() (*AccountService, *LedgerService, *TrasnsactionService) {
	as := NewAccountService()
	ls := NewLedgerService()
	ts := NewTransactionService(ls, as)
	return as, ls, ts
}

func createAccount(as *AccountService, id string, amount int, currency CURRENCY) {
	as.account[id] = &Account{
		accountId: id,
		balance: Money{
			amount:   amount,
			currency: currency,
		},
		RWMutex: sync.RWMutex{},
	}
}

func TestTransferSuccess(t *testing.T) {

	as, ls, ts := setup()

	createAccount(as, "A", 100, INR)
	createAccount(as, "B", 50, INR)

	tr, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   30,
			currency: INR,
		},
	)

	require.NoError(t, err)

	require.Equal(t, SUCCESS, tr.status)

	require.Equal(t, 70, as.account["A"].balance.amount)
	require.Equal(t, 80, as.account["B"].balance.amount)

	require.Len(t, ls.l.entries, 2)

}

func TestTransferInsufficientBalance(t *testing.T) {

	as, ls, ts := setup()

	createAccount(as, "A", 10, INR)
	createAccount(as, "B", 50, INR)

	_, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   30,
			currency: INR,
		},
	)

	require.Error(t, err)

	require.Equal(t, 10, as.account["A"].balance.amount)
	require.Equal(t, 50, as.account["B"].balance.amount)

	require.Len(t, ls.l.entries, 0)

}

func TestTransferCurrencyMismatch(t *testing.T) {

	as, ls, ts := setup()

	createAccount(as, "A", 100, INR)
	createAccount(as, "B", 50, USD)

	_, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   30,
			currency: INR,
		},
	)

	require.Error(t, err)

	require.Equal(t, 100, as.account["A"].balance.amount)
	require.Equal(t, 50, as.account["B"].balance.amount)

	require.Len(t, ls.l.entries, 0)

}

func TestDuplicateTransaction(t *testing.T) {

	as, _, ts := setup()

	createAccount(as, "A", 100, INR)
	createAccount(as, "B", 100, INR)

	_, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   10,
			currency: INR,
		},
	)

	require.NoError(t, err)

	tr, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   10,
			currency: INR,
		},
	)

	require.Equal(t, tr.status, SUCCESS)

}

func TestConcurrentTransfersMoneyConservation(t *testing.T) {

	as, _, ts := setup()

	createAccount(as, "A", 1000, INR)
	createAccount(as, "B", 1000, INR)

	initialTotal :=
		as.account["A"].balance.amount +
			as.account["B"].balance.amount

	wg := sync.WaitGroup{}

	for i := 0; i < 100; i++ {

		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, err := ts.Transfer(
				fmt.Sprintf("txn-%d", i),
				"A",
				"B",
				Money{
					amount:   1,
					currency: INR,
				},
			)

			require.NoError(t, err)

		}(i)
	}

	wg.Wait()

	finalTotal :=
		as.account["A"].balance.amount +
			as.account["B"].balance.amount

	require.Equal(t, initialTotal, finalTotal)

	require.Equal(t, 900, as.account["A"].balance.amount)
	require.Equal(t, 1100, as.account["B"].balance.amount)

}

func TestConcurrentBidirectionalTransfersNoDeadlock(t *testing.T) {

	as, _, ts := setup()

	createAccount(as, "A", 1000, INR)
	createAccount(as, "B", 1000, INR)

	wg := sync.WaitGroup{}

	for i := 0; i < 100; i++ {

		wg.Add(2)

		go func(i int) {
			defer wg.Done()

			_, err := ts.Transfer(
				fmt.Sprintf("ab-%d", i),
				"A",
				"B",
				Money{
					amount:   1,
					currency: INR,
				},
			)

			require.NoError(t, err)

		}(i)

		go func(i int) {
			defer wg.Done()

			_, err := ts.Transfer(
				fmt.Sprintf("ba-%d", i),
				"B",
				"A",
				Money{
					amount:   1,
					currency: INR,
				},
			)

			require.NoError(t, err)

		}(i)
	}

	wg.Wait()

	total :=
		as.account["A"].balance.amount +
			as.account["B"].balance.amount

	require.Equal(t, 2000, total)

}

func TestTransactionStateTransition(t *testing.T) {

	as, _, ts := setup()

	createAccount(as, "A", 100, INR)
	createAccount(as, "B", 100, INR)

	tr, err := ts.Transfer(
		"txn-1",
		"A",
		"B",
		Money{
			amount:   10,
			currency: INR,
		},
	)

	require.NoError(t, err)

	err = ts.updateTxnStatus(tr.txnId, PENDING)

	require.Error(t, err)

}
