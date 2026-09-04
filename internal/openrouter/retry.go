package openrouter

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRetryBudget = 30 * time.Second
	baseBackoff        = 500 * time.Millisecond
	maxBackoff         = 10 * time.Second
)

type outcome int

const (
	done outcome = iota
	retry
	fatal
)

func classify(status int, err error) outcome {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fatal
		}
		if status == 0 {
			return retry
		}
	}
	if status >= 200 && status < 300 {
		if err != nil {
			return fatal
		}
		return done
	}
	if status == 408 || status == 429 || status >= 500 {
		return retry
	}
	return fatal
}

func parseRetryAfter(value string) (time.Duration, bool) {
	seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}
	if seconds > uint64(maxBackoff/time.Second) {
		return maxBackoff, true
	}
	return time.Duration(seconds) * time.Second, true
}

func backoffCeiling(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= 63 {
		return maxBackoff
	}
	factor := uint64(1) << attempt
	if factor > uint64(maxBackoff/baseBackoff) {
		return maxBackoff
	}
	return min(time.Duration(factor)*baseBackoff, maxBackoff)
}

func equalJitter(ceiling time.Duration) time.Duration {
	half := ceiling / 2
	if half == 0 {
		return ceiling
	}
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	if delay, ok := parseRetryAfter(retryAfter); ok {
		return delay
	}
	return equalJitter(backoffCeiling(attempt))
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
