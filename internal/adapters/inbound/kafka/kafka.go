// Package kafka holds this service's INBOUND Kafka consumers (ADR 0003): one
// Consumer per consumed topic (vendor-connectivity, shipping-and-tracking,
// delivery-promise, offer-availability, node-topology). Every consumer decodes
// CloudEvents 1.0 only, via internal/adapters/kafka/cloudevents, and dispatches
// on the FULL `type` string.
//
// Delivery guarantee: AT-LEAST-ONCE with an ATOMIC effect. Each message is
// handled by ONE intake use case, which claims the CloudEvents id in
// processed_events and applies the change in one unit of work. The offset is
// committed only AFTER handling returned nil (FetchMessage + CommitMessages).
// Handling returns an error for a transient failure: the run loop retries the
// SAME message with capped exponential backoff and, after a bounded number of
// attempts, dead-letters it to "<topic>.dlq" so a stuck message cannot wedge the
// partition. A payload that can never be applied is dead-lettered at once; a
// message that is not a CloudEvents 1.0 event, or whose type has no handler, is
// skipped.
package kafka

import (
	"context"
	"errors"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Reader is the subset of *kafkago.Reader a consumer needs, so unit tests never
// need a live broker. FetchMessage/CommitMessages (not ReadMessage) because
// ReadMessage with a GroupID auto-commits the offset the moment it returns,
// before the message has been handled.
type Reader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// readerConfig builds the kafka-go reader configuration. GroupID always comes
// from the caller (an env var at the composition root, never a literal).
// CommitInterval stays UNSET: with a GroupID kafka-go then commits synchronously
// inside CommitMessages, which the loop calls only after success.
func readerConfig(brokers []string, topic, groupID string) kafkago.ReaderConfig {
	return kafkago.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: groupID}
}

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// RetryPolicy is the capped exponential backoff the run loop applies while the
// SAME message keeps failing with a transient error. Zero fields fall back to
// DefaultRetryInitial / DefaultRetryMax.
type RetryPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

// Default backoff: 200ms, 400ms, 800ms ... capped at 5s.
const (
	DefaultRetryInitial = 200 * time.Millisecond
	DefaultRetryMax     = 5 * time.Second
)

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.Initial <= 0 {
		p.Initial = DefaultRetryInitial
	}
	if p.Max <= 0 {
		p.Max = DefaultRetryMax
	}
	return p
}

// next doubles d, capped at p.Max.
func (p RetryPolicy) next(d time.Duration) time.Duration {
	return min(d*2, p.Max)
}

// sleepFunc waits d or until ctx is done (returning ctx.Err()). A field so tests
// can record the backoff sequence without really sleeping.
type sleepFunc func(ctx context.Context, d time.Duration) error

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// consumeLoop is the at-least-once run loop shared by every consumer.
type consumeLoop struct {
	reader Reader
	// handle applies one message; non-nil means transient failure => retry,
	// unless it wraps errPoison.
	handle func(ctx context.Context, msg kafkago.Message) error
	logger *slog.Logger
	name   string
	retry  RetryPolicy
	sleep  sleepFunc

	// maxAttempts and deadLetter bound the retry: once handle failed
	// maxAttempts times for the SAME message, deadLetter is called instead of
	// retrying again.
	maxAttempts int
	deadLetter  func(ctx context.Context, msg kafkago.Message, cause error) error
}

// run fetches one message at a time and does not fetch the next until the
// current one was handled AND its offset committed. It returns only when ctx
// is cancelled or FetchMessage itself fails.
func (l *consumeLoop) run(ctx context.Context) error {
	policy := l.retry.withDefaults()
	sleep := l.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for {
		msg, err := l.reader.FetchMessage(ctx)
		if err != nil {
			return err
		}
		if err := l.handleWithDeadLetter(ctx, policy, sleep, msg); err != nil {
			return err
		}
		// Commit ONLY now. A commit that fails is retried too: the work is
		// already durable, and if the process dies first the redelivery is
		// skipped by the processed-event claim.
		if err := l.retryUntilOK(ctx, policy, sleep, "offset commit", msg, func() error {
			return l.reader.CommitMessages(ctx, msg)
		}); err != nil {
			return err
		}
	}
}

// handleWithDeadLetter retries l.handle on msg with capped backoff, up to
// l.maxAttempts, then publishes msg to the dead-letter sink. A poison message is
// dead-lettered at once. The dead-letter PUBLISH itself is retried forever: a
// message that could not even be dead-lettered must never be dropped.
func (l *consumeLoop) handleWithDeadLetter(ctx context.Context, p RetryPolicy, sleep sleepFunc, msg kafkago.Message) error {
	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := l.handle(ctx, msg)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errPoison) {
			l.logger.ErrorContext(ctx, l.name+" message can never be applied; dead-lettering it without retry",
				"error", err, "partition", msg.Partition, "offset", msg.Offset)
			return l.publishDeadLetter(ctx, p, sleep, msg, err)
		}
		if attempt >= l.maxAttempts {
			l.logger.ErrorContext(ctx, l.name+" handling failed after the maximum attempts; dead-lettering the message",
				"error", err, "partition", msg.Partition, "offset", msg.Offset, "attempts", attempt)
			return l.publishDeadLetter(ctx, p, sleep, msg, err)
		}
		l.logger.ErrorContext(ctx, l.name+" handling failed; retrying the same message",
			"error", err, "partition", msg.Partition, "offset", msg.Offset,
			"attempt", attempt, "max_attempts", l.maxAttempts, "retry_in", delay)
		if serr := sleep(ctx, delay); serr != nil {
			return serr
		}
		delay = p.next(delay)
	}
}

func (l *consumeLoop) publishDeadLetter(ctx context.Context, p RetryPolicy, sleep sleepFunc, msg kafkago.Message, cause error) error {
	return l.retryUntilOK(ctx, p, sleep, "dead-letter publish", msg, func() error {
		return l.deadLetter(ctx, msg, cause)
	})
}

// retryUntilOK runs op until it returns nil, sleeping with capped exponential
// backoff between attempts. It gives up (returning ctx.Err()) only when ctx is
// cancelled. It NEVER skips: a drop is silent data loss.
func (l *consumeLoop) retryUntilOK(ctx context.Context, p RetryPolicy, sleep sleepFunc, what string, msg kafkago.Message, op func() error) error {
	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := op()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.logger.ErrorContext(ctx, l.name+" "+what+" failed; retrying the same message",
			"error", err, "partition", msg.Partition, "offset", msg.Offset,
			"attempt", attempt, "retry_in", delay)
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		delay = p.next(delay)
	}
}
