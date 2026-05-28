package mcoding

import "errors"

var (
	ErrTxnInFlight       = errors.New("transaction in flight")
	ErrTxnFailed         = errors.New("transaction previously failed; use new txnId")
	ErrAccountNotFound   = errors.New("account not found")
	ErrCurrencyMistmatch = errors.New("currency mismatch")
	ErrInsufficientFunds = errors.New("Insufficient funds")
)
