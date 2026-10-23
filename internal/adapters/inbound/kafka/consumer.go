package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

// errBadPayload marks a CloudEvent whose `data` cannot be decoded: a
// deterministic problem, never retried.
var errBadPayload = errors.New("malformed event payload")

// errPoison marks a message the consumer can never apply (malformed payload, a
// field that breaks a value rule): the loop dead-letters it at once, without
// retrying, and commits past it.
var errPoison = errors.New("poison message")

// HandlerFunc applies one decoded CloudEvent. nil means done; a non-nil error
// is transient unless it wraps errBadPayload or usecases.ErrInvalidEvent.
type HandlerFunc func(ctx context.Context, e ce.Event) error

// Consumer reads one producer topic under a stable consumer group and dispatches
// each message on the FULL CloudEvents `type` to its handler. Types it has no
// handler for are ignored.
type Consumer struct {
	Name     string
	Topic    string
	Reader   Reader
	Handlers map[string]HandlerFunc
	Logger   *slog.Logger
	Retry    RetryPolicy

	// DLQ is the dead-letter writer (topic Topic + DLQSuffix): a transient
	// failure is retried maxHandlerAttempts times before the message is
	// published there instead of blocking the partition forever, and an event
	// that can never be applied goes there at once.
	DLQ DeadLetterWriter

	sleep sleepFunc // test hook; nil => real, ctx-cancellable sleep
}

// newConsumer builds a Consumer reading topic from brokers under groupID.
// Constructing the reader and the writer does not dial.
func newConsumer(name string, brokers []string, topic, groupID string, handlers map[string]HandlerFunc, logger *slog.Logger) *Consumer {
	return &Consumer{
		Name: name, Topic: topic, Handlers: handlers, Logger: defaultLogger(logger),
		Reader: kafkago.NewReader(readerConfig(brokers, topic, groupID)),
		DLQ:    newDLQWriter(brokers),
	}
}

// Run consumes until ctx is cancelled or the reader fails.
func (c *Consumer) Run(ctx context.Context) error {
	loop := consumeLoop{
		reader:      c.Reader,
		handle:      func(ctx context.Context, msg kafkago.Message) error { return c.HandleMessage(ctx, msg.Value) },
		logger:      defaultLogger(c.Logger),
		name:        c.Name,
		retry:       c.Retry,
		sleep:       c.sleep,
		maxAttempts: maxHandlerAttempts,
		deadLetter: func(ctx context.Context, msg kafkago.Message, cause error) error {
			return publishDeadLetter(ctx, c.DLQ, c.Topic+DLQSuffix, msg, cause)
		},
	}
	return loop.run(ctx)
}

// Close releases the reader and the dead-letter writer.
func (c *Consumer) Close() error {
	err := c.Reader.Close()
	if c.DLQ != nil {
		err = errors.Join(err, c.DLQ.Close())
	}
	return err
}

// HandleMessage decodes one CloudEvents 1.0 message and dispatches it. It
// returns nil for what is skipped (logged at WARN: not a valid CloudEvent;
// silently: a type this consumer does not act on), an error wrapping errPoison
// for an event that can never be applied, and any other error for a transient
// failure, after the unit of work rolled back.
func (c *Consumer) HandleMessage(ctx context.Context, value []byte) error {
	logger := defaultLogger(c.Logger)
	e, err := cloudevents.Decode(value)
	if err != nil {
		logger.WarnContext(ctx, "skipping a message that is not a valid CloudEvent", "consumer", c.Name, "topic", c.Topic, "error", err)
		return nil
	}
	handle, ok := c.Handlers[e.Type()]
	if !ok {
		return nil
	}
	err = handle(ctx, e)
	if !errors.Is(err, errBadPayload) && !errors.Is(err, usecases.ErrInvalidEvent) {
		return err
	}
	logger.WarnContext(ctx, "dead-lettering an event that can never be applied", "consumer", c.Name, "type", e.Type(), "event_id", e.ID(), "error", err)
	return fmt.Errorf("%w: %w", errPoison, err)
}

// decodeData decodes e's JSON `data` into a T.
func decodeData[T any](e ce.Event) (T, error) {
	var v T
	if err := e.DataAs(&v); err != nil {
		return v, fmt.Errorf("%w: %v", errBadPayload, err)
	}
	return v, nil
}

// logOutcome records what an intake use case did.
func logOutcome(ctx context.Context, logger *slog.Logger, e ce.Event, outcome usecases.Outcome) {
	logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "subject", e.Subject(), "outcome", string(outcome))
}
