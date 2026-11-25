package sync

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryStopsOnPermanent(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), Policy{Attempts: 5, Base: time.Millisecond, Max: time.Millisecond}, func(context.Context) error {
		calls++
		return ErrPermanent
	})
	if !errors.Is(err, ErrPermanent) || calls != 1 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}
