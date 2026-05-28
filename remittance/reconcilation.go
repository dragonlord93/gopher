package remittance

import (
	"context"
	"fmt"
	"time"
)

// ============================================================
// Reconciliation Service
//
// Responsibility: resolve UNKNOWN states to KNOWN states.
// That's it. It never drives business flow (payout, refund).
//
// When it resolves a state, it notifies the listener
// (TransactionStateListener), which decides what to do next.
// The listener is the orchestrator, but recon doesn't know that.
//
// Dependency graph:
//   ReconciliationService → TransactionService
//                         → DebitProvider / PayoutProvider (GetTxnStatus only)
//                         → TransactionStateListener (interface)
//                                    ↑
//                              Orchestrator (injected at startup)
// ============================================================

type ReconciliationService struct {
	txnService         TransactionService
	debitProviders     map[string]DebitProvider
	payoutProviders    map[string]PayoutProvider
	listener           TransactionStateListener
	pollInterval       time.Duration
	maxPendingDuration time.Duration
}

func NewReconciliationService(
	txnService TransactionService,
	debitProviders map[string]DebitProvider,
	payoutProviders map[string]PayoutProvider,
	listener TransactionStateListener,
) *ReconciliationService {
	return &ReconciliationService{
		txnService:         txnService,
		debitProviders:     debitProviders,
		payoutProviders:    payoutProviders,
		listener:           listener,
		pollInterval:       5 * time.Minute,
		maxPendingDuration: 72 * time.Hour,
	}
}

// Start launches the reconciliation loop. Cancel ctx to stop.
func (r *ReconciliationService) Start(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("reconciliation: shutting down")
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *ReconciliationService) reconcile(ctx context.Context) {
	r.resolveStuckDebits(ctx)
	r.resolveStuckPayoutInitiated(ctx)
	r.resolvePayoutPending(ctx)
}

// ============================================================
// DEBIT_INITIATED: we called debit provider but never got a
// response. Check what actually happened.
// ============================================================

func (r *ReconciliationService) resolveStuckDebits(ctx context.Context) {
	txns, err := r.txnService.FindByStatus(ctx, DEBIT_INITIATED)
	if err != nil {
		fmt.Printf("recon: error finding DEBIT_INITIATED txns: %v\n", err)
		return
	}

	for _, txn := range txns {
		provider, ok := r.debitProviders[txn.DebitProviderID]
		if !ok {
			fmt.Printf("recon: unknown debit provider %s for txn %s\n", txn.DebitProviderID, txn.TxnID)
			continue
		}

		// No provider ref → we crashed before the request left. Debit never happened.
		if txn.DebitProviderTxnRef == "" {
			r.transitionAndNotify(ctx, txn, DEBIT_FAILED, "debit never sent (no provider ref)")
			continue
		}

		status, err := provider.GetTxnStatus(ctx, txn.DebitProviderTxnRef)
		if err != nil {
			fmt.Printf("recon: error checking debit for txn %s: %v\n", txn.TxnID, err)
			continue // retry next cycle
		}

		switch status {
		case ProviderTxnSuccess:
			// Debit confirmed → notify listener to continue payout leg
			r.transitionAndNotify(ctx, txn, DEBITED, "")

		case ProviderTxnFailed:
			r.transitionAndNotify(ctx, txn, DEBIT_FAILED, "debit failed (confirmed by provider)")

		case ProviderTxnPending, ProviderTxnUnknown:
			if time.Since(txn.CreatedAt) > r.maxPendingDuration {
				r.transitionAndNotify(ctx, txn, DEBIT_FAILED, "debit timed out after 72h")
			}
			// Otherwise wait for next cycle
		}
	}
}

// ============================================================
// PAYOUT_INITIATED: we called payout provider but don't know
// if they received it. Check — do NOT re-initiate.
// ============================================================

func (r *ReconciliationService) resolveStuckPayoutInitiated(ctx context.Context) {
	txns, err := r.txnService.FindByStatus(ctx, PAYOUT_INITIATED)
	if err != nil {
		fmt.Printf("recon: error finding PAYOUT_INITIATED txns: %v\n", err)
		return
	}

	for _, txn := range txns {
		provider, ok := r.payoutProviders[txn.PayoutProviderID]
		if !ok {
			fmt.Printf("recon: unknown payout provider %s for txn %s\n", txn.PayoutProviderID, txn.TxnID)
			continue
		}

		// No provider ref → crashed before request left. Payout never happened.
		// Notify listener → it will trigger refund.
		if txn.PayoutProviderTxnRef == "" {
			r.transitionAndNotify(ctx, txn, PAYOUT_FAILED, "payout never sent (no provider ref)")
			continue
		}

		status, err := provider.GetTxnStatus(ctx, txn.PayoutProviderTxnRef)
		if err != nil {
			fmt.Printf("recon: error checking payout for txn %s: %v\n", txn.TxnID, err)
			continue
		}

		switch status {
		case ProviderTxnSuccess:
			r.transitionAndNotify(ctx, txn, PAYOUT_PENDING, "")

		case ProviderTxnFailed:
			// Notify listener → it will trigger refund
			r.transitionAndNotify(ctx, txn, PAYOUT_FAILED, "payout failed (confirmed by provider)")

		case ProviderTxnPending, ProviderTxnUnknown:
			if time.Since(txn.CreatedAt) > r.maxPendingDuration {
				// 72h timeout → force fail. Accept dual-pay risk.
				r.transitionAndNotify(ctx, txn, PAYOUT_FAILED, "payout timed out after 72h, force refund")
			}
		}
	}
}

// ============================================================
// PAYOUT_PENDING: provider accepted, awaiting settlement.
// ============================================================

func (r *ReconciliationService) resolvePayoutPending(ctx context.Context) {
	txns, err := r.txnService.FindByStatus(ctx, PAYOUT_PENDING)
	if err != nil {
		fmt.Printf("recon: error finding PAYOUT_PENDING txns: %v\n", err)
		return
	}

	for _, txn := range txns {
		provider, ok := r.payoutProviders[txn.PayoutProviderID]
		if !ok {
			continue
		}

		status, err := provider.GetTxnStatus(ctx, txn.PayoutProviderTxnRef)
		if err != nil {
			continue
		}

		switch status {
		case ProviderTxnSuccess:
			r.transitionAndNotify(ctx, txn, SETTLED, "")

		case ProviderTxnFailed:
			// Notify listener → it will trigger refund
			r.transitionAndNotify(ctx, txn, PAYOUT_FAILED, "payout failed after acceptance")

		case ProviderTxnPending:
			if time.Since(txn.CreatedAt) > r.maxPendingDuration {
				r.transitionAndNotify(ctx, txn, PAYOUT_FAILED, "settlement timed out after 72h")
			}

		default:
			// Unknown — wait for next cycle
		}
	}
}

// ============================================================
// Core pattern: transition state, then notify listener.
//
// Recon resolves: "this txn is now in state X"
// Listener decides: "state X means I should do Y"
//
// Recon never knows what Y is. Clean separation.
// ============================================================

func (r *ReconciliationService) transitionAndNotify(ctx context.Context, txn *Transaction, to TransactionStatus, reason string) {
	from := txn.Status

	err := r.txnService.UpdateTransaction(ctx, &UpdateTransactionReq{
		TxnID:         txn.TxnID,
		Status:        to,
		FailureReason: reason,
	})
	if err != nil {
		fmt.Printf("recon: failed to transition txn %s from %d to %d: %v\n", txn.TxnID, from, to, err)
		return
	}

	// Notify the listener (orchestrator) about the state change.
	// The listener drives the next step (payout, refund, etc.)
	if r.listener != nil {
		if err := r.listener.OnStateChange(ctx, txn.TxnID, from, to); err != nil {
			fmt.Printf("recon: listener error for txn %s (%d→%d): %v\n", txn.TxnID, from, to, err)
			// Non-fatal — next recon cycle will find the txn in its new state
			// and the orchestrator can retry via OnStateChange again.
		}
	}
}
