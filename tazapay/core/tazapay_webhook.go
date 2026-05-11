package tazapay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dragonlord93/gopher/pubsub"
)

type Event struct {
	ProviderId string
	EventId    string
	EventType  string
	Payload    string
	ReceivedAt time.Time
}

type TPWebhook struct {
	pbsb pubsub.PubSub // We could just keep publisher
}

func NewTpWebHook(ctx context.Context, pbsb pubsub.PubSub) *TPWebhook {
	return &TPWebhook{
		pbsb: pbsb,
	}
}

func (tp *TPWebhook) Webhook(e Event) error {
	msg, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tp.pbsb.Publish(context.Background(), e.EventType, msg)
	return nil
}
