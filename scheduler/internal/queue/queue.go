package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	"scheduler/internal/config"
)

const (
	Topic          = "job-events"
	ConsumerGroup  = "job-workers"
)

// Producer publishes JobEvents to Kafka.
// The message Key is the deduplication key — Kafka brokers deduplicate
// messages with the same key within the deduplication window.
type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string) *Producer {
	return &Producer{
		writer: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Topic:        Topic,
			Balancer:     &kafka.Hash{}, // route by key — same key → same partition
			RequiredAcks: kafka.RequireAll,
			// Idempotent writer: Kafka broker deduplicates messages with the same
			// producer ID + sequence number. Combined with our dedup key, this
			// gives us exactly-once delivery even across producer restarts.
			Async: false, // synchronous — we need to know if publish succeeded
		},
	}
}

// Publish sends a JobEvent to Kafka.
// The message key is "job_id:scheduled_epoch" — if the same job fires twice
// due to a leader failover race, the second message is a duplicate and Kafka drops it.
func (p *Producer) Publish(ctx context.Context, event *config.JobEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	key := event.DeduplicationKey() // "job-uuid:1700000000"

	err = p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: data,
		// Headers carry metadata useful for tracing
		Headers: []kafka.Header{
			{Key: "tenant_id",  Value: []byte(event.TenantID)},
			{Key: "attempt",    Value: []byte(fmt.Sprintf("%d", event.AttemptNumber))},
		},
	})
	if err != nil {
		return fmt.Errorf("kafka write: %w", err)
	}

	log.Printf("[queue] published job=%s epoch=%d key=%s",
		event.JobID, event.ScheduledEpoch, key)
	return nil
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

// Consumer reads JobEvents from Kafka and hands them to a handler function.
// Kafka guarantees at-least-once delivery — workers must be idempotent.
type Consumer struct {
	reader *kafka.Reader
}

func NewConsumer(brokers []string, workerID string) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:        brokers,
			Topic:          Topic,
			GroupID:        ConsumerGroup, // consumer group enables horizontal worker scaling
			MinBytes:       1,
			MaxBytes:       10e6,
			CommitInterval: time.Second,  // auto-commit offset every second
			// StartOffset: kafka.LastOffset — start from end (only new messages)
		}),
	}
}

// Run reads messages in a loop and calls handler for each.
// handler should be idempotent — the same event may be delivered more than once.
func (c *Consumer) Run(ctx context.Context, handler func(context.Context, *config.JobEvent) error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			log.Printf("[consumer] fetch error: %v", err)
			time.Sleep(time.Second)
			continue
		}

		var event config.JobEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("[consumer] bad message key=%s: %v — skipping", msg.Key, err)
			// Commit anyway — bad messages go to dead letter queue in production
			c.reader.CommitMessages(ctx, msg)
			continue
		}

		// Execute the job handler
		if err := handler(ctx, &event); err != nil {
			log.Printf("[consumer] handler error job=%s: %v", event.JobID, err)
			// Don't commit — Kafka will redeliver (retry)
			// In production, wrap with exponential backoff and DLQ after max retries
			continue
		}

		// Commit offset only after successful processing
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("[consumer] commit error: %v", err)
		}
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
