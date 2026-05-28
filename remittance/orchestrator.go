package remittance

import (
	"context"
	"fmt"
	"time"
)

// ============================================================
// Orchestrator
//
// Drives the transfer lifecycle. Also implements
// TransactionStateListener so the reconciliation service
// can trigger continuation of orphaned flows without
// knowing about the orchestrator directly.
// ============================================================

type Orchestrator struct {
	fxService          FXService
	txnService         TransactionService
	debitRouter        DebitRouter
	payoutRouter       PayoutRouter
	userService        UserService
	beneficiaryService BeneficiaryService
}

// Compile-time check: Orchestrator implements TransactionStateListener
var _ TransactionStateListener = (*Orchestrator)(nil)

func NewOrchestrator(
	fxService FXService,
	txnService TransactionService,
	debitRouter DebitRouter,
	payoutRouter PayoutRouter,
	userService UserService,
	beneficiaryService BeneficiaryService,
) *Orchestrator {
	return &Orchestrator{
		fxService:          fxService,
		txnService:         txnService,
		debitRouter:        debitRouter,
		payoutRouter:       payoutRouter,
		userService:        userService,
		beneficiaryService: beneficiaryService,
	}
}

// ============================================================
// TransactionStateListener implementation
//
// Called by the reconciliation service when it resolves an
// unknown state to a known state. The orchestrator decides
// what to do next. Recon never drives business logic itself.
//
// Dependency graph (no cycle):
//   Reconciliation → TransactionStateListener (interface)
//   Orchestrator implements TransactionStateListener
//   Orchestrator is injected into Reconciliation at startup
// ============================================================

func (o *Orchestrator) OnStateChange(ctx context.Context, txnID string, from, to TransactionStatus) error {
	switch {

	// Recon resolved a stuck debit → now drive the payout leg
	case from == DEBIT_INITIATED && to == DEBITED:
		return o.ExecutePayoutLeg(ctx, txnID)

	// Recon resolved a payout to failed → trigger refund
	case to == PAYOUT_FAILED:
		return o.ExecuteRefundLeg(ctx, txnID)

	// Recon resolved payout pending → settled. Nothing more to do.
	case to == SETTLED:
		fmt.Printf("orchestrator: txn %s settled via reconciliation\n", txnID)
		return nil

	default:
		return nil
	}
}

// ============================================================
// GetQuote — phase 1 of the user flow
// ============================================================

func (o *Orchestrator) GetQuote(ctx context.Context, senderID string, amount Money) (*FXQuote, error) {
	if amount.Amount <= 0 {
		return nil, fmt.Errorf("orchestrator: send amount must be positive")
	}

	user, err := o.userService.GetUser(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: invalid sender: %w", err)
	}
	if !user.KycVerified {
		return nil, fmt.Errorf("orchestrator: sender KYC not verified")
	}
	if amount.Currency != user.Currency {
		return nil, fmt.Errorf("orchestrator: currency mismatch: got %d, expected %d", amount.Currency, user.Currency)
	}

	return o.fxService.GetQuote(ctx, QuoteRequest{
		SenderAmount: amount,
		ToCurrency:   INR,
	})
}

// ============================================================
// InitiateTransfer — phase 2 of the user flow
// Drives: validation → create txn → debit leg → payout leg
// ============================================================

type InitiateTransferRequest struct {
	TxnID       string
	QuoteID     string
	SenderID    string
	RecipientID string
}

func (o *Orchestrator) InitiateTransfer(ctx context.Context, req InitiateTransferRequest) (*Transaction, error) {

	// ----------------------------------------------------------
	// Step 1: Validate sender
	// ----------------------------------------------------------
	user, err := o.userService.GetUser(ctx, req.SenderID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: invalid sender: %w", err)
	}
	if !user.KycVerified {
		return nil, fmt.Errorf("orchestrator: sender KYC not verified")
	}

	// ----------------------------------------------------------
	// Step 2: Validate beneficiary belongs to sender
	// ----------------------------------------------------------
	beneficiary, err := o.beneficiaryService.GetBeneficiary(ctx, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: invalid beneficiary: %w", err)
	}
	if beneficiary.OwnerUserID != req.SenderID {
		return nil, fmt.Errorf("orchestrator: beneficiary does not belong to sender")
	}

	// ----------------------------------------------------------
	// Step 3: Fetch and validate quote
	// ----------------------------------------------------------
	quote, err := o.fxService.FetchQuote(ctx, req.QuoteID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: quote fetch failed: %w", err)
	}
	if quote.Used {
		return nil, fmt.Errorf("orchestrator: quote already used")
	}
	if time.Now().After(quote.ExpiresAt) {
		return nil, fmt.Errorf("orchestrator: quote expired")
	}

	// ----------------------------------------------------------
	// Step 4: Create transaction in INITIATED state
	// Idempotency: same txnID + same payload → existing txn
	//              same txnID + diff payload → error (422)
	// ----------------------------------------------------------
	txn, err := o.txnService.InitiateTransaction(ctx, TransactionReq{
		TxnID:           req.TxnID,
		QuoteID:         req.QuoteID,
		SenderID:        req.SenderID,
		RecipientID:     req.RecipientID,
		SenderAmount:    quote.SendAmount,
		RecipientAmount: quote.ReceiveAmount,
		Fee:             quote.Fee,
		FxRate:          quote.FxRate,
	})
	if err != nil {
		return nil, fmt.Errorf("orchestrator: create transaction failed: %w", err)
	}

	// Idempotent retry — txn already past INITIATED, return as-is
	if txn.Status != INITIATED {
		return txn, nil
	}

	// ----------------------------------------------------------
	// Step 5: Execute debit leg
	// ----------------------------------------------------------
	if err := o.executeDebitLeg(ctx, req.TxnID, quote, user); err != nil {
		txn, _ = o.txnService.GetTransaction(ctx, req.TxnID)
		return txn, err
	}

	// ----------------------------------------------------------
	// Step 6: Execute payout leg
	// ----------------------------------------------------------
	if err := o.ExecutePayoutLeg(ctx, req.TxnID); err != nil {
		txn, _ = o.txnService.GetTransaction(ctx, req.TxnID)
		return txn, err
	}

	// ----------------------------------------------------------
	// Step 7: Mark quote as used
	// ----------------------------------------------------------
	_ = o.fxService.MarkUsed(ctx, req.QuoteID)

	// ----------------------------------------------------------
	// Step 8: Return transaction in current state
	// ----------------------------------------------------------
	txn, _ = o.txnService.GetTransaction(ctx, req.TxnID)
	return txn, nil
}

// ============================================================
// GetTransfer — query current state
// ============================================================

func (o *Orchestrator) GetTransfer(ctx context.Context, txnID string) (*Transaction, error) {
	return o.txnService.GetTransaction(ctx, txnID)
}

// ============================================================
// Debit Leg (private — only called from InitiateTransfer)
// ============================================================

func (o *Orchestrator) executeDebitLeg(ctx context.Context, txnID string, quote *FXQuote, user *User) error {

	// Route to debit provider
	debitProvider, err := o.debitRouter.Route(ctx, user.Corridor)
	if err != nil {
		o.markFailed(ctx, txnID, DEBIT_FAILED, "no debit provider available")
		return fmt.Errorf("orchestrator: debit routing failed: %w", err)
	}

	// Write-ahead: mark DEBIT_INITIATED before the external call
	err = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:           txnID,
		Status:          DEBIT_INITIATED,
		DebitProviderID: debitProvider.ID(),
	})
	if err != nil {
		return fmt.Errorf("orchestrator: failed to mark debit_initiated: %w", err)
	}

	// Call debit provider — three-way outcome
	debitRes, err := debitProvider.Debit(ctx, DebitRequest{
		TxnID:       txnID,
		Amount:      quote.SendAmount,
		FromAccount: user.FundingSourceID,
		RequestedAt: time.Now(),
	})

	if err != nil {
		// UNKNOWN — leave as DEBIT_INITIATED. Recon will call GetTxnStatus.
		return fmt.Errorf("orchestrator: debit unknown outcome (recon will resolve): %w", err)
	}

	if debitRes.Status == ProviderTxnFailed {
		// EXPLICIT failure — safe to mark failed
		o.markFailed(ctx, txnID, DEBIT_FAILED, debitRes.FailureReason)
		return fmt.Errorf("orchestrator: debit rejected: %s", debitRes.FailureReason)
	}

	// SUCCESS
	return o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:               txnID,
		Status:              DEBITED,
		DebitProviderTxnRef: debitRes.ProviderTxnRef,
	})
}

// ============================================================
// Payout Leg (public — called from InitiateTransfer AND
// from OnStateChange when recon resolves a stuck debit)
// ============================================================

func (o *Orchestrator) ExecutePayoutLeg(ctx context.Context, txnID string) error {

	// Fetch transaction to get recipient + amounts
	txn, err := o.txnService.GetTransaction(ctx, txnID)
	if err != nil {
		return fmt.Errorf("orchestrator: payout leg: txn not found: %w", err)
	}

	// Guard: only proceed if transaction is in DEBITED state
	if txn.Status != DEBITED {
		return fmt.Errorf("orchestrator: payout leg: unexpected state %d for txn %s", txn.Status, txnID)
	}

	// Resolve beneficiary for bank details
	beneficiary, err := o.beneficiaryService.GetBeneficiary(ctx, txn.RecipientID)
	if err != nil {
		o.initiateRefund(ctx, txnID, "beneficiary not found during payout")
		return fmt.Errorf("orchestrator: payout leg: beneficiary error: %w", err)
	}

	// Route to payout provider
	payoutProvider, err := o.payoutRouter.Route(ctx, txn.RecipientAmount, beneficiary.IFSC)
	if err != nil {
		o.initiateRefund(ctx, txnID, "no payout provider available")
		return fmt.Errorf("orchestrator: payout routing failed, refund initiated: %w", err)
	}

	// Write-ahead: mark PAYOUT_INITIATED
	err = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:            txnID,
		Status:           PAYOUT_INITIATED,
		PayoutProviderID: payoutProvider.ID(),
	})
	if err != nil {
		o.initiateRefund(ctx, txnID, "failed to mark payout_initiated")
		return fmt.Errorf("orchestrator: failed to mark payout_initiated: %w", err)
	}

	// Call payout provider — three-way outcome
	payoutRes, err := payoutProvider.Payout(ctx, PayoutRequest{
		TxnID:       txnID,
		Amount:      txn.RecipientAmount,
		ToAccount:   beneficiary.BankAccount,
		IFSC:        beneficiary.IFSC,
		RequestedAt: time.Now(),
	})

	if err != nil {
		// UNKNOWN — leave as PAYOUT_INITIATED. Recon will check.
		// Do NOT refund — payout may have succeeded.
		return fmt.Errorf("orchestrator: payout unknown outcome (recon will resolve): %w", err)
	}

	if payoutRes.Status == ProviderTxnFailed {
		// EXPLICIT failure — safe to refund
		_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
			TxnID:                txnID,
			Status:               PAYOUT_FAILED,
			PayoutProviderTxnRef: payoutRes.ProviderTxnRef,
			FailureReason:        payoutRes.FailureReason,
		})
		o.initiateRefund(ctx, txnID, payoutRes.FailureReason)
		return fmt.Errorf("orchestrator: payout rejected: %s", payoutRes.FailureReason)
	}

	// SUCCESS — payout accepted, awaiting settlement
	return o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:                txnID,
		Status:               PAYOUT_PENDING,
		PayoutProviderTxnRef: payoutRes.ProviderTxnRef,
	})
}

// ============================================================
// Refund Leg (public — called from OnStateChange and payout)
//
// In production: calls debitProvider.Refund(debitProviderTxnRef).
// Here: marks the state. Recon drives actual refund + confirmation.
// ============================================================

func (o *Orchestrator) ExecuteRefundLeg(ctx context.Context, txnID string) error {
	txn, err := o.txnService.GetTransaction(ctx, txnID)
	if err != nil {
		return err
	}

	// Must be in PAYOUT_FAILED to initiate refund
	if txn.Status != PAYOUT_FAILED {
		return fmt.Errorf("orchestrator: refund leg: unexpected state %d for txn %s", txn.Status, txnID)
	}

	return o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:  txnID,
		Status: REFUND_INITIATED,
	})
}

// ============================================================
// Helpers
// ============================================================

func (o *Orchestrator) markFailed(ctx context.Context, txnID string, status TransactionStatus, reason string) {
	_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:         txnID,
		Status:        status,
		FailureReason: reason,
	})
}

func (o *Orchestrator) initiateRefund(ctx context.Context, txnID string, reason string) {
	txn, err := o.txnService.GetTransaction(ctx, txnID)
	if err != nil {
		return
	}

	// Walk through required intermediate states to reach REFUND_INITIATED.
	// State machine enforces: DEBITED → PAYOUT_INITIATED → PAYOUT_FAILED → REFUND_INITIATED
	// Some of these may already be set; UpdateTransaction will reject invalid transitions.
	if txn.Status == DEBITED {
		_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
			TxnID:         txnID,
			Status:        PAYOUT_INITIATED,
			FailureReason: reason,
		})
		_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
			TxnID:         txnID,
			Status:        PAYOUT_FAILED,
			FailureReason: reason,
		})
	}

	if txn.Status == PAYOUT_INITIATED || txn.Status == PAYOUT_PENDING {
		_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
			TxnID:         txnID,
			Status:        PAYOUT_FAILED,
			FailureReason: reason,
		})
	}

	_ = o.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:         txnID,
		Status:        REFUND_INITIATED,
		FailureReason: reason,
	})
}
