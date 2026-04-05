package pubsub

/*

LLD interview preparation and would like to practice a pub sub system in golang with correct concurrency model.

- Actors:
Client publishing message to a topic via interface
Client subscribing to a topic to receive message
Client can Add Topic
Client can remove topic
*/

import (
	"context"
)

type SubscribeCallbackFn func(msg []byte) error

type Publisher interface {
	Publish(ctx context.Context, topicName string, message []byte) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, topicName string, callback SubscribeCallbackFn) (subID string, err error)
	Unsubscribe(ctx context.Context, topicName string, subID string) error
}

type TopicMgmt interface {
	AddTopic(ctx context.Context, topicName string) error
	RemoveTopic(ctx context.Context, topicName string) error
}

type PubSub interface {
	Publisher
	Subscriber
	TopicMgmt
}
