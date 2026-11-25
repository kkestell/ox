package sync

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// ErrPermanent marks a failure retrying cannot fix.
var ErrPermanent = errors.New("permanent failure")

// Op is one attempt at a request.
type Op func(context.Context) error

// Policy bounds how a request is retried.
type Policy struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultPolicy backs off from 200ms up to 10s.
var DefaultPolicy = Policy{
	Attempts: 5,
	Base:     200 * time.Millisecond,
	Max:      10 * time.Second,
}

// Retry runs op until it succeeds or fails for good.
func Retry(ctx context.Context, p Policy, op Op) error {
	var err error
	for attempt := range p.Attempts {
		err = op(ctx)
		if err == nil || errors.Is(err, ErrPermanent) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.backoff(attempt)):
		}
	}
	return err
}

// backoff is full jitter up to a capped exponential.
func (p Policy) backoff(attempt int) time.Duration {
	bound := min(p.Base<<attempt, p.Max)
	return rand.N(bound) + 1
}
