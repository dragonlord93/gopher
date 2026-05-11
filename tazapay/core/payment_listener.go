package tazapay

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dragonlord93/gopher/pubsub"
)

const (
	maxBatchSize = 10
	flushEvery   = 500 * time.Millisecond
)

type PaymentListener struct {
	eventLock *sync.RWMutex
	pbsub     pubsub.PubSub
	subId     string
	events    map[string]Event

	bufferLock sync.Mutex
	buffer     []Event
}

func SubscribePaymentEvents(ctx context.Context, pbsb pubsub.PubSub) (*PaymentListener, error) {
	pl := &PaymentListener{
		pbsub:     pbsb,
		eventLock: &sync.RWMutex{},
		events:    make(map[string]Event),
	}
	subId, err := pl.pbsub.Subscribe(ctx, "PAYMENT", pl.processEvents)
	pl.subId = subId
	if err != nil {
		return nil, fmt.Errorf("cannot start listening for payment events")
	}

	// Batch the events upto a buffer and flush periodically
	go pl.startFlushTicker(ctx)

	return pl, nil
}

func (pl *PaymentListener) Unsubscribe(ctx context.Context) {
	pl.pbsub.Unsubscribe(ctx, "PAYMENT", pl.subId)
}

// processEvents buffers incoming events and flushes when the batch is full.
func (pl *PaymentListener) processEvents(msg []byte) error {
	event := Event{}
	if err := json.Unmarshal(msg, &event); err != nil {
		return err
	}

	pl.bufferLock.Lock()
	pl.buffer = append(pl.buffer, event)
	shouldFlush := len(pl.buffer) >= maxBatchSize
	pl.bufferLock.Unlock()

	if shouldFlush {
		pl.flush()
	}
	return nil
}

func (pl *PaymentListener) startFlushTicker(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pl.flush()
		case <-ctx.Done():
			pl.flush()
			return
		}
	}
}

func (pl *PaymentListener) flush() {
	pl.bufferLock.Lock()
	if len(pl.buffer) == 0 {
		pl.bufferLock.Unlock()
		return
	}
	batch := pl.buffer
	pl.buffer = nil
	pl.bufferLock.Unlock()

	pl.eventLock.Lock()
	defer pl.eventLock.Unlock()
	for _, event := range batch {
		key := fmt.Sprintf("%s#%s", event.ProviderId, event.EventId)
		if _, exists := pl.events[key]; !exists {
			pl.events[key] = event
		}
	}
}

func (pl *PaymentListener) ListEvents() {
	pl.eventLock.RLock()
	defer pl.eventLock.RUnlock()
	for _, val := range pl.events {
		fmt.Printf("EventId: %s, ProviderId: %s, EventType: %s, ReceivedAt: %v\n", val.EventId, val.ProviderId, val.EventType, val.ReceivedAt)
	}
}
