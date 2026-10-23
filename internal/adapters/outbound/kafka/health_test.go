package kafka

import (
	"context"
	"errors"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

type fakeLister struct {
	brokers []kafkago.Broker
	err     error
}

func (f fakeLister) Brokers(context.Context) ([]kafkago.Broker, error) { return f.brokers, f.err }

func TestBrokerCheck(t *testing.T) {
	ok := NewBrokerCheckWithLister(fakeLister{brokers: []kafkago.Broker{{ID: 1}}})
	if err := ok.Check(context.Background()); err != nil {
		t.Fatalf("a broker answered: %v", err)
	}
	boom := errors.New("refused")
	if err := NewBrokerCheckWithLister(fakeLister{err: boom}).Check(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if err := NewBrokerCheckWithLister(fakeLister{}).Check(context.Background()); err == nil {
		t.Fatal("an answer that lists no broker is not ready")
	}
}

func TestDialListerWithoutAddressesOrWithADeadOne(t *testing.T) {
	if _, err := (dialLister{}).Brokers(context.Background()); err == nil {
		t.Fatal("no address configured must be an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewBrokerCheck([]string{"127.0.0.1:1"}).Check(ctx); err == nil {
		t.Fatal("a dead broker is not ready")
	}
}
