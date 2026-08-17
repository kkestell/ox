package agent

import (
	"context"
	"testing"
)

func TestSessionClaimAdmitsExactlyOneConcurrentTurn(t *testing.T) {
	const claimants = 32

	value := &session{}
	type result struct {
		release func() bool
		err     error
	}
	results := make(chan result, claimants)
	start := make(chan struct{})
	for range claimants {
		go func() {
			<-start
			_, release, err := value.claim(context.Background())
			results <- result{release: release, err: err}
		}()
	}
	close(start)

	var winner func() bool
	refused := 0
	for range claimants {
		result := <-results
		if result.err != nil {
			refused++
			continue
		}
		if winner != nil {
			t.Fatal("more than one concurrent claim succeeded")
		}
		winner = result.release
	}
	if winner == nil {
		t.Fatal("no concurrent claim succeeded")
	}
	defer winner()
	if refused != claimants-1 {
		t.Fatalf("refused claims = %d, want %d", refused, claimants-1)
	}
}
