package openrouter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryClassifier(t *testing.T) {
	transportError := errors.New("connection reset")
	if classify(0, transportError) != retry {
		t.Fatal("transport error is not retryable")
	}
	for _, status := range []int{408, 429, 500, 502, 503} {
		if classify(status, errors.New("request failed")) != retry {
			t.Fatalf("status %d is not retryable", status)
		}
	}
	for _, status := range []int{400, 401, 402, 403} {
		if classify(status, errors.New("request failed")) != fatal {
			t.Fatalf("status %d is not fatal", status)
		}
	}
	if classify(200, errors.New("invalid JSON")) != fatal {
		t.Fatal("2xx parse failure is not fatal")
	}
	if classify(0, context.Canceled) != fatal {
		t.Fatal("cancellation is not fatal")
	}
}

func TestRetryAfterAcceptsIntegerSecondsAndCaps(t *testing.T) {
	if got, ok := parseRetryAfter(" 2 "); !ok || got != 2*time.Second {
		t.Fatalf("retry after = %v, %v", got, ok)
	}
	if got, ok := parseRetryAfter("60"); !ok || got != maxBackoff {
		t.Fatalf("capped retry after = %v, %v", got, ok)
	}
	if _, ok := parseRetryAfter("Sun, 26 Jul 2026 12:00:00 GMT"); ok {
		t.Fatal("HTTP-date Retry-After was accepted")
	}
}

func TestBackoffSaturatesAndEqualJitterStaysInBounds(t *testing.T) {
	if backoffCeiling(0) != baseBackoff ||
		backoffCeiling(1) != 2*baseBackoff ||
		backoffCeiling(1000) != maxBackoff {
		t.Fatalf(
			"ceilings = %v, %v, %v",
			backoffCeiling(0),
			backoffCeiling(1),
			backoffCeiling(1000),
		)
	}
	for range 100 {
		delay := equalJitter(2 * time.Second)
		if delay < time.Second || delay > 2*time.Second {
			t.Fatalf("jittered delay = %v", delay)
		}
	}
}
