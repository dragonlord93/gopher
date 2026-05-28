package remittance

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ============================================================
// In-Memory Transaction Service
// Owns: idempotency, state machine validation, storage
// ============================================================

type InMemoryTransactionService struct {
	mu          sync.RWMutex
	store       map[string]*Transaction                   // txnID → Transaction
	statusIndex map[TransactionStatus]map[string]struct{} // status → set of txnIDs
}

func NewTransactionService() *InMemoryTransactionService {
	svc := &InMemoryTransactionService{
		store:       make(map[string]*Transaction),
		statusIndex: make(map[TransactionStatus]map[string]struct{}),
	}
	return svc
}

func (s *InMemoryTransactionService) InitiateTransaction(ctx context.Context, req TransactionReq) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotency check: if txnID exists, validate payload match
	if existing, ok := s.store[req.TxnID]; ok {
		if existing.QuoteID != req.QuoteID ||
			existing.SenderID != req.SenderID ||
			existing.RecipientID != req.RecipientID {
			// Same key, different payload → 422 contract violation
			return nil, fmt.Errorf("idempotency_key_reuse: txnID %s already attached to a different request (original quote: %s)",
				req.TxnID, existing.QuoteID)
		}
		// Same key, same payload → return existing (idempotent retry)
		return existing, nil
	}

	now := time.Now()
	txn := &Transaction{
		TxnID:           req.TxnID,
		QuoteID:         req.QuoteID,
		SenderID:        req.SenderID,
		RecipientID:     req.RecipientID,
		SenderAmount:    req.SenderAmount,
		RecipientAmount: req.RecipientAmount,
		Fee:             req.Fee,
		FxRate:          req.FxRate,
		Status:          INITIATED,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.store[req.TxnID] = txn
	s.addToIndex(INITIATED, req.TxnID)

	return txn, nil
}

func (s *InMemoryTransactionService) UpdateTransaction(ctx context.Context, req *UpdateTransactionReq) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	txn, ok := s.store[req.TxnID]
	if !ok {
		return fmt.Errorf("transaction %s not found", req.TxnID)
	}

	// Validate state machine transition
	if !CanTransition(txn.Status, req.Status) {
		return fmt.Errorf("invalid transition: %d → %d for txn %s", txn.Status, req.Status, req.TxnID)
	}

	oldStatus := txn.Status

	// Apply updates
	txn.Status = req.Status
	txn.UpdatedAt = time.Now()

	if req.DebitProviderID != "" {
		txn.DebitProviderID = req.DebitProviderID
	}
	if req.DebitProviderTxnRef != "" {
		txn.DebitProviderTxnRef = req.DebitProviderTxnRef
	}
	if req.PayoutProviderID != "" {
		txn.PayoutProviderID = req.PayoutProviderID
	}
	if req.PayoutProviderTxnRef != "" {
		txn.PayoutProviderTxnRef = req.PayoutProviderTxnRef
	}
	if req.FailureReason != "" {
		txn.FailureReason = req.FailureReason
	}

	// Update secondary index
	s.removeFromIndex(oldStatus, req.TxnID)
	s.addToIndex(req.Status, req.TxnID)

	return nil
}

func (s *InMemoryTransactionService) GetTransaction(ctx context.Context, txnID string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	txn, ok := s.store[txnID]
	if !ok {
		return nil, fmt.Errorf("transaction %s not found", txnID)
	}
	return txn, nil
}

// FindByStatus returns all transactions in a given status.
// Used by the reconciliation service to find DEBIT_INITIATED, PAYOUT_INITIATED,
// PAYOUT_PENDING transactions that need status checks.
func (s *InMemoryTransactionService) FindByStatus(ctx context.Context, status TransactionStatus) ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	txnIDs, ok := s.statusIndex[status]
	if !ok {
		return nil, nil
	}

	result := make([]*Transaction, 0, len(txnIDs))
	for txnID := range txnIDs {
		if txn, exists := s.store[txnID]; exists {
			result = append(result, txn)
		}
	}
	return result, nil
}

// ============================================================
// Secondary index helpers
// ============================================================

func (s *InMemoryTransactionService) addToIndex(status TransactionStatus, txnID string) {
	if _, ok := s.statusIndex[status]; !ok {
		s.statusIndex[status] = make(map[string]struct{})
	}
	s.statusIndex[status][txnID] = struct{}{}
}

func (s *InMemoryTransactionService) removeFromIndex(status TransactionStatus, txnID string) {
	if ids, ok := s.statusIndex[status]; ok {
		delete(ids, txnID)
		if len(ids) == 0 {
			delete(s.statusIndex, status)
		}
	}
}
