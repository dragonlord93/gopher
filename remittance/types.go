package remittance

import (
	"context"
	"fmt"
	"time"
)

// ============================================================
// Currency & Money
// ============================================================

type Currency int

const (
	INR Currency = iota
	GBP
	USD
	AED
	EUR
)

type Money struct {
	Amount   int // smallest denomination (paise, pence, cents)
	Currency Currency
}

func NewMoney(amount int, currency Currency) (Money, error) {
	if amount < 0 {
		return Money{}, fmt.Errorf("money: amount cannot be negative")
	}
	return Money{Amount: amount, Currency: currency}, nil
}

// ============================================================
// Transaction Status & State Machine
// ============================================================

type TransactionStatus int

const (
	INITIATED TransactionStatus = iota
	DEBIT_INITIATED
	DEBITED
	DEBIT_FAILED
	PAYOUT_INITIATED
	PAYOUT_PENDING
	PAYOUT_FAILED
	SETTLED
	REFUND_INITIATED
	REFUNDED
	REFUND_FAILED
)

var validTransitions = map[TransactionStatus][]TransactionStatus{
	INITIATED:        {DEBIT_INITIATED},
	DEBIT_INITIATED:  {DEBITED, DEBIT_FAILED},
	DEBITED:          {PAYOUT_INITIATED},
	PAYOUT_INITIATED: {PAYOUT_PENDING, PAYOUT_FAILED},
	PAYOUT_PENDING:   {SETTLED, PAYOUT_FAILED},
	PAYOUT_FAILED:    {REFUND_INITIATED},
	REFUND_INITIATED: {REFUNDED, REFUND_FAILED},
}

func CanTransition(from, to TransactionStatus) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// ============================================================
// Domain Entities
// ============================================================

type Transaction struct {
	TxnID                string // client-supplied, doubles as idempotency key
	SenderID             string
	RecipientID          string
	QuoteID              string
	SenderAmount         Money
	RecipientAmount      Money
	Fee                  Money
	FxRate               float64
	Status               TransactionStatus
	DebitProviderID      string
	DebitProviderTxnRef  string
	PayoutProviderID     string
	PayoutProviderTxnRef string
	FailureReason        string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type FXQuote struct {
	QuoteID       string
	SendAmount    Money
	ReceiveAmount Money
	Fee           Money
	FxRate        float64
	FromCurrency  Currency
	ToCurrency    Currency
	ExpiresAt     time.Time
	Used          bool
	CreatedAt     time.Time
}

type Corridor int

const (
	CorridorUK Corridor = iota
	CorridorUS
	CorridorUAE
	CorridorEU
)

type User struct {
	UserID          string
	Name            string
	Corridor        Corridor
	KycVerified     bool
	FundingSourceID string
	Currency        Currency
}

type Beneficiary struct {
	BeneficiaryID string
	OwnerUserID   string
	Name          string
	BankAccount   string
	IFSC          string
}

// ============================================================
// Provider Interfaces
// ============================================================

type ProviderTxnStatus int

const (
	ProviderTxnSuccess ProviderTxnStatus = iota
	ProviderTxnFailed
	ProviderTxnPending
	ProviderTxnUnknown
)

type DebitRequest struct {
	TxnID       string
	Amount      Money
	FromAccount string
	RequestedAt time.Time
}

type DebitResponse struct {
	ProviderTxnRef string
	Status         ProviderTxnStatus
	FailureReason  string
}

type PayoutRequest struct {
	TxnID       string
	Amount      Money
	ToAccount   string
	IFSC        string
	RequestedAt time.Time
}

type PayoutResponse struct {
	ProviderTxnRef string
	Status         ProviderTxnStatus
	FailureReason  string
}

type DebitProvider interface {
	ID() string
	Debit(ctx context.Context, req DebitRequest) (DebitResponse, error)
	GetTxnStatus(ctx context.Context, providerTxnRef string) (ProviderTxnStatus, error)
}

type PayoutProvider interface {
	ID() string
	Payout(ctx context.Context, req PayoutRequest) (PayoutResponse, error)
	GetTxnStatus(ctx context.Context, providerTxnRef string) (ProviderTxnStatus, error)
}

// ============================================================
// Router Interfaces
// ============================================================

type DebitRouter interface {
	Route(ctx context.Context, corridor Corridor) (DebitProvider, error)
}

type PayoutRouter interface {
	Route(ctx context.Context, amount Money, ifsc string) (PayoutProvider, error)
}

// ============================================================
// Service Interfaces
// ============================================================

type TransactionReq struct {
	TxnID           string
	QuoteID         string
	SenderID        string
	RecipientID     string
	SenderAmount    Money
	RecipientAmount Money
	Fee             Money
	FxRate          float64
}

type UpdateTransactionReq struct {
	TxnID                string
	Status               TransactionStatus
	DebitProviderID      string
	DebitProviderTxnRef  string
	PayoutProviderID     string
	PayoutProviderTxnRef string
	FailureReason        string
}

type TransactionService interface {
	// Creates transaction in INITIATED state. Returns existing txn if txnID already exists.
	// Returns error if txnID exists with different payload (idempotency violation).
	InitiateTransaction(ctx context.Context, req TransactionReq) (*Transaction, error)

	// Validates state transition and updates transaction.
	UpdateTransaction(ctx context.Context, req *UpdateTransactionReq) error

	// Retrieves transaction by ID.
	GetTransaction(ctx context.Context, txnID string) (*Transaction, error)

	// Returns all transactions in a given status (used by reconciliation).
	FindByStatus(ctx context.Context, status TransactionStatus) ([]*Transaction, error)
}

type QuoteRequest struct {
	SenderAmount Money
	ToCurrency   Currency
}

type FXService interface {
	// Creates a new quote with a TTL. Stores it internally.
	GetQuote(ctx context.Context, req QuoteRequest) (*FXQuote, error)

	// Fetches an existing quote. Returns error if not found.
	FetchQuote(ctx context.Context, quoteID string) (*FXQuote, error)

	// Marks quote as consumed. Returns error if already used.
	MarkUsed(ctx context.Context, quoteID string) error
}

type UserService interface {
	GetUser(ctx context.Context, userID string) (*User, error)
}

type BeneficiaryService interface {
	GetBeneficiary(ctx context.Context, beneficiaryID string) (*Beneficiary, error)
}

// ============================================================
// State Change Listener
// Decouples reconciliation from orchestration.
// Reconciliation resolves unknown → known state.
// Listener (implemented by orchestrator) drives the next step.
// ============================================================

type TransactionStateListener interface {
	OnStateChange(ctx context.Context, txnID string, from, to TransactionStatus) error
}
