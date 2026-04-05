package pubsub

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testConf() *PubSubConf {
	return &PubSubConf{
		TopicChangeTimeout: 2 * time.Second,
		SubChangeTimeout:   2 * time.Second,
		PublishTimeout:     2 * time.Second,
		TopicChanSize:      100,
		SubChanSize:        100,
		PublishChanSize:    100,
	}
}

func newTestPubSub(t *testing.T) (PubSub, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ps := NewPubSubImpl(ctx, testConf())
	return ps, cancel
}

// ========================
// Topic Management Tests
// ========================

func TestAddTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	err := ps.AddTopic(context.Background(), "orders")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestAddDuplicateTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")
	err := ps.AddTopic(context.Background(), "orders")
	if err == nil {
		t.Fatal("expected error for duplicate topic, got nil")
	}
}

func TestRemoveTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")
	err := ps.RemoveTopic(context.Background(), "orders")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRemoveNonExistentTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	err := ps.RemoveTopic(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for non-existent topic, got nil")
	}
}

// ========================
// Subscribe / Unsubscribe Tests
// ========================

func TestSubscribe(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")
	subID, err := ps.Subscribe(context.Background(), "orders", func(msg []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if subID == "" {
		t.Fatal("expected non-empty subID")
	}
}

func TestSubscribeToNonExistentTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_, err := ps.Subscribe(context.Background(), "ghost", func(msg []byte) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error subscribing to non-existent topic, got nil")
	}
}

func TestUnsubscribe(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")
	subID, _ := ps.Subscribe(context.Background(), "orders", func(msg []byte) error {
		return nil
	})

	err := ps.Unsubscribe(context.Background(), "orders", subID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUnsubscribeInvalidSubID(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")
	err := ps.Unsubscribe(context.Background(), "orders", "bogus-id")
	if err == nil {
		t.Fatal("expected error for invalid subID, got nil")
	}
}

func TestUnsubscribeFromNonExistentTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	err := ps.Unsubscribe(context.Background(), "ghost", "some-id")
	if err == nil {
		t.Fatal("expected error for non-existent topic, got nil")
	}
}

// ========================
// Publish Tests
// ========================

func TestPublishSingleSubscriber(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")

	received := make(chan []byte, 1)
	_, _ = ps.Subscribe(context.Background(), "orders", func(msg []byte) error {
		received <- msg
		return nil
	})

	err := ps.Publish(context.Background(), "orders", []byte("hello"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	select {
	case msg := <-received:
		if string(msg) != "hello" {
			t.Fatalf("expected 'hello', got %q", string(msg))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestPublishMultipleSubscribers(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "events")

	subscriberCount := 5
	var wg sync.WaitGroup
	var count atomic.Int32

	for i := 0; i < subscriberCount; i++ {
		wg.Add(1)
		_, _ = ps.Subscribe(context.Background(), "events", func(msg []byte) error {
			count.Add(1)
			wg.Done()
			return nil
		})
	}

	_ = ps.Publish(context.Background(), "events", []byte("broadcast"))

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if count.Load() != int32(subscriberCount) {
			t.Fatalf("expected %d callbacks, got %d", subscriberCount, count.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out: only %d of %d subscribers received message", count.Load(), subscriberCount)
	}
}

func TestPublishToNonExistentTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	err := ps.Publish(context.Background(), "ghost", []byte("hello"))
	if err == nil {
		t.Fatal("expected error publishing to non-existent topic, got nil")
	}
}

func TestPublishAfterUnsubscribe(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "orders")

	called := atomic.Bool{}
	subID, _ := ps.Subscribe(context.Background(), "orders", func(msg []byte) error {
		called.Store(true)
		return nil
	})

	_ = ps.Unsubscribe(context.Background(), "orders", subID)
	_ = ps.Publish(context.Background(), "orders", []byte("should not arrive"))

	// give goroutine a chance to fire (it shouldn't)
	time.Sleep(100 * time.Millisecond)
	if called.Load() {
		t.Fatal("callback should not have been invoked after unsubscribe")
	}
}

func TestPublishToRemovedTopic(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "temp")
	_ = ps.RemoveTopic(context.Background(), "temp")

	err := ps.Publish(context.Background(), "temp", []byte("hello"))
	if err == nil {
		t.Fatal("expected error publishing to removed topic, got nil")
	}
}

// ========================
// Message Isolation Test
// ========================

func TestMessageCopyIsolation(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	_ = ps.AddTopic(context.Background(), "data")

	received := make(chan []byte, 2)

	// subscriber 1: mutates the message
	_, _ = ps.Subscribe(context.Background(), "data", func(msg []byte) error {
		msg[0] = 'X' // mutate
		received <- msg
		return nil
	})

	// subscriber 2: should see original message, not mutated
	_, _ = ps.Subscribe(context.Background(), "data", func(msg []byte) error {
		received <- msg
		return nil
	})

	_ = ps.Publish(context.Background(), "data", []byte("hello"))

	var messages []string
	for i := 0; i < 2; i++ {
		select {
		case msg := <-received:
			messages = append(messages, string(msg))
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for messages")
		}
	}

	// one will be "Xello" (the mutator), the other must be "hello"
	foundOriginal := false
	for _, m := range messages {
		if m == "hello" {
			foundOriginal = true
		}
	}
	if !foundOriginal {
		t.Fatalf("message mutation leaked across subscribers: got %v", messages)
	}
}

// ========================
// Context Cancellation Tests
// ========================

func TestContextCancellationStopsManager(t *testing.T) {
	ps, cancel := newTestPubSub(t)

	_ = ps.AddTopic(context.Background(), "orders")

	cancel() // stop the manager

	// give manager goroutine time to exit
	time.Sleep(100 * time.Millisecond)

	// operations should now fail via timeout (manager is gone)
	ctx, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	err := ps.AddTopic(ctx, "new-topic")
	if err == nil {
		t.Fatal("expected error after context cancellation, got nil")
	}
}

func TestOperationWithCancelledContext(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	ctx, cancelOp := context.WithCancel(context.Background())
	cancelOp() // cancel immediately

	err := ps.AddTopic(ctx, "orders")
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

// ========================
// Concurrent Stress Test
// ========================

func TestConcurrentOperations(t *testing.T) {
	ps, cancel := newTestPubSub(t)
	defer cancel()

	ctx := context.Background()
	topicCount := 10
	subsPerTopic := 5

	// create topics concurrently
	var wg sync.WaitGroup
	for i := 0; i < topicCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			topicName := fmt.Sprintf("topic-%d", idx)
			if err := ps.AddTopic(ctx, topicName); err != nil {
				t.Errorf("failed to add topic %s: %v", topicName, err)
			}
		}(i)
	}
	wg.Wait()

	// subscribe concurrently
	var msgCount atomic.Int32
	var subWg sync.WaitGroup
	for i := 0; i < topicCount; i++ {
		for j := 0; j < subsPerTopic; j++ {
			subWg.Add(1)
			go func(idx int) {
				defer subWg.Done()
				topicName := fmt.Sprintf("topic-%d", idx)
				_, err := ps.Subscribe(ctx, topicName, func(msg []byte) error {
					msgCount.Add(1)
					return nil
				})
				if err != nil {
					t.Errorf("failed to subscribe to %s: %v", topicName, err)
				}
			}(i)
		}
	}
	subWg.Wait()

	// publish concurrently
	var pubWg sync.WaitGroup
	for i := 0; i < topicCount; i++ {
		pubWg.Add(1)
		go func(idx int) {
			defer pubWg.Done()
			topicName := fmt.Sprintf("topic-%d", idx)
			if err := ps.Publish(ctx, topicName, []byte("stress")); err != nil {
				t.Errorf("failed to publish to %s: %v", topicName, err)
			}
		}(i)
	}
	pubWg.Wait()

	// wait for all callbacks
	time.Sleep(500 * time.Millisecond)

	expected := int32(topicCount * subsPerTopic)
	got := msgCount.Load()
	if got != expected {
		t.Fatalf("expected %d total messages delivered, got %d", expected, got)
	}
}

// ========================
// Race Detector Test
// ========================

func TestNoRaceOnPublish(t *testing.T) {
	// This test is meaningful when run with -race flag
	// go test -race ./...
	ps, cancel := newTestPubSub(t)
	defer cancel()

	ctx := context.Background()
	_ = ps.AddTopic(ctx, "race-topic")

	for i := 0; i < 10; i++ {
		_, _ = ps.Subscribe(ctx, "race-topic", func(msg []byte) error {
			return nil
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = ps.Publish(ctx, "race-topic", []byte(fmt.Sprintf("msg-%d", idx)))
		}(i)
	}
	wg.Wait()
}
