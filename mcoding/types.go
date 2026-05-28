package mcoding

type CURRENCY int

const (
	INR CURRENCY = iota
	USD
	GBP
)

func (c CURRENCY) String() string {
	switch c {
	case INR:
		return "INR"
	case USD:
		return "USD"
	case GBP:
		return "GBP"
	default:
		return "Unknown"
	}
}

type Money struct {
	amount   int
	currency CURRENCY
}

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMistmatch
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMistmatch
	}
	if m.amount < other.amount {
		return Money{}, ErrInsufficientFunds
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}
