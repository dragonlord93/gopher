package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dragonlord93/gopher/pubsub"
	tazapay "github.com/dragonlord93/gopher/tazapay/core"
)

func main() {
	ctx := context.Background()
	pbsb := pubsub.NewPubSubImpl(ctx, pubsub.DefaultPubSubConf())
	pbsb.AddTopic(ctx, "PAYMENT")
	wehbook := tazapay.NewTpWebHook(ctx, pbsb)
	pl, err := tazapay.SubscribePaymentEvents(ctx, pbsb)
	if err != nil {
		panic(err)
	}
	wg := &sync.WaitGroup{}

	// Send 100 events concurrently with "ProviderId: ABC and EventId: 123
	// only 1 event should exist in list of events.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wehbook.Webhook(tazapay.Event{
				ProviderId: "ABC",
				EventId:    "123",
				Payload:    "",
				EventType:  "PAYMENT",
				ReceivedAt: time.Now(),
			})
		}()
	}
	wg.Wait()
	pl.ListEvents()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
}
