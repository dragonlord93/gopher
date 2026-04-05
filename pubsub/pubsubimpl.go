package pubsub

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type topicOp int
type subOp int

const (
	addTopic topicOp = iota
	removeTopic
)

const (
	subscribe subOp = iota
	unsubscribe
)

// unexported — internal implementation detail
type topic struct {
	subscribers map[string]SubscribeCallbackFn
}

type modifyTopicOp struct {
	topicName string
	op        topicOp
	resp      chan error // added: response channel for confirmation
}

type modifySubOp struct {
	topicName string
	op        subOp
	cbFn      SubscribeCallbackFn
	resp      chan *subOpChanResponse
	subId     string
}

type publishOp struct {
	topicName string
	msg       []byte
	resp      chan error
}

type subOpChanResponse struct {
	subId string
	err   error
}

type PubSubConf struct {
	TopicChangeTimeout time.Duration
	SubChangeTimeout   time.Duration
	PublishTimeout     time.Duration
	TopicChanSize      int
	SubChanSize        int
	PublishChanSize    int
}

func DefaultPubSubConf() *PubSubConf {
	return &PubSubConf{
		TopicChangeTimeout: 5 * time.Second,
		SubChangeTimeout:   5 * time.Second,
		PublishTimeout:     5 * time.Second,
		TopicChanSize:      100,
		SubChanSize:        100,
		PublishChanSize:    100,
	}
}

type pubSubImpl struct {
	topics     map[string]*topic
	topicChan  chan *modifyTopicOp
	subChan    chan *modifySubOp
	publishChn chan *publishOp // added: publish goes through actor too
	conf       *PubSubConf
}

func NewPubSubImpl(ctx context.Context, conf *PubSubConf) PubSub {
	ps := &pubSubImpl{
		topics:     make(map[string]*topic),
		topicChan:  make(chan *modifyTopicOp, conf.TopicChanSize),
		subChan:    make(chan *modifySubOp, conf.SubChanSize),
		publishChn: make(chan *publishOp, conf.PublishChanSize),
		conf:       conf,
	}
	go ps.pubsubMgr(ctx)
	return ps
}

func (ps *pubSubImpl) AddTopic(ctx context.Context, topicName string) error {
	op := &modifyTopicOp{
		topicName: topicName,
		op:        addTopic,
		resp:      make(chan error, 1),
	}
	select {
	case ps.topicChan <- op:
	case <-time.After(ps.conf.TopicChangeTimeout):
		return fmt.Errorf("cannot add topic: timed out sending operation")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-op.resp:
		return err
	case <-time.After(ps.conf.TopicChangeTimeout):
		return fmt.Errorf("cannot add topic: timed out waiting for response")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ps *pubSubImpl) RemoveTopic(ctx context.Context, topicName string) error {
	op := &modifyTopicOp{
		topicName: topicName,
		op:        removeTopic,
		resp:      make(chan error, 1),
	}
	select {
	case ps.topicChan <- op:
	case <-time.After(ps.conf.TopicChangeTimeout):
		return fmt.Errorf("cannot remove topic: timed out sending operation")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-op.resp:
		return err
	case <-time.After(ps.conf.TopicChangeTimeout):
		return fmt.Errorf("cannot remove topic: timed out waiting for response")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ps *pubSubImpl) Publish(ctx context.Context, topicName string, msg []byte) error {
	op := &publishOp{
		topicName: topicName,
		msg:       msg,
		resp:      make(chan error, 1),
	}
	select {
	case ps.publishChn <- op:
	case <-time.After(ps.conf.PublishTimeout):
		return fmt.Errorf("cannot publish: timed out sending operation")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-op.resp:
		return err
	case <-time.After(ps.conf.PublishTimeout):
		return fmt.Errorf("cannot publish: timed out waiting for response")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ps *pubSubImpl) Subscribe(ctx context.Context, topicName string,
	callbackFn SubscribeCallbackFn) (subID string, err error) {
	op := &modifySubOp{
		topicName: topicName,
		op:        subscribe,
		cbFn:      callbackFn,
		resp:      make(chan *subOpChanResponse, 1),
	}
	select {
	case ps.subChan <- op:
	case <-time.After(ps.conf.SubChangeTimeout):
		return "", fmt.Errorf("cannot subscribe: timed out sending operation")
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// fixed: removed unnecessary for loop — single response expected
	select {
	case res := <-op.resp:
		return res.subId, res.err
	case <-time.After(ps.conf.SubChangeTimeout):
		return "", fmt.Errorf("cannot subscribe: timed out waiting for response")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (ps *pubSubImpl) Unsubscribe(ctx context.Context, topicName string, subID string) error {
	op := &modifySubOp{
		topicName: topicName,
		op:        unsubscribe,
		subId:     subID,
		resp:      make(chan *subOpChanResponse, 1),
	}
	select {
	case ps.subChan <- op:
	case <-time.After(ps.conf.SubChangeTimeout):
		return fmt.Errorf("cannot unsubscribe: timed out sending operation")
	case <-ctx.Done():
		return ctx.Err()
	}

	// fixed: removed unnecessary for loop
	select {
	case res := <-op.resp:
		return res.err
	case <-time.After(ps.conf.SubChangeTimeout):
		return fmt.Errorf("cannot unsubscribe: timed out waiting for response")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ps *pubSubImpl) pubsubMgr(ctx context.Context) {
	for {
		select {
		case modTopicOp := <-ps.topicChan:
			switch modTopicOp.op {
			case addTopic:
				if _, ok := ps.topics[modTopicOp.topicName]; ok {
					modTopicOp.resp <- fmt.Errorf("topic %q already exists", modTopicOp.topicName)
					continue
				}
				ps.topics[modTopicOp.topicName] = &topic{subscribers: make(map[string]SubscribeCallbackFn)}
				modTopicOp.resp <- nil

			case removeTopic:
				if _, ok := ps.topics[modTopicOp.topicName]; !ok {
					modTopicOp.resp <- fmt.Errorf("topic %q does not exist", modTopicOp.topicName)
					continue
				}
				delete(ps.topics, modTopicOp.topicName)
				modTopicOp.resp <- nil
			}

		case pubOp := <-ps.publishChn:
			t, ok := ps.topics[pubOp.topicName]
			if !ok {
				pubOp.resp <- fmt.Errorf("topic %q does not exist", pubOp.topicName)
				continue
			}
			// fixed: copy message per subscriber to prevent mutation corruption
			for _, fn := range t.subscribers {
				msgCopy := make([]byte, len(pubOp.msg))
				copy(msgCopy, pubOp.msg)
				go fn(msgCopy)
			}
			pubOp.resp <- nil

		case modSubOp := <-ps.subChan:
			switch modSubOp.op {
			case subscribe:
				if _, ok := ps.topics[modSubOp.topicName]; !ok {
					modSubOp.resp <- &subOpChanResponse{"", fmt.Errorf("topic %q does not exist", modSubOp.topicName)}
					continue
				}
				t := ps.topics[modSubOp.topicName]
				subId := uuid.New().String()
				t.subscribers[subId] = modSubOp.cbFn
				modSubOp.resp <- &subOpChanResponse{subId, nil}

			case unsubscribe:
				t, ok := ps.topics[modSubOp.topicName]
				if !ok {
					modSubOp.resp <- &subOpChanResponse{"", fmt.Errorf("topic %q does not exist", modSubOp.topicName)}
					continue
				}
				if _, ok := t.subscribers[modSubOp.subId]; !ok {
					modSubOp.resp <- &subOpChanResponse{"", fmt.Errorf("subscription %q does not exist", modSubOp.subId)}
					continue
				}
				delete(t.subscribers, modSubOp.subId)
				modSubOp.resp <- &subOpChanResponse{"", nil}
			}

		case <-ctx.Done():
			return
		}
	}
}

var _ PubSub = (*pubSubImpl)(nil)
