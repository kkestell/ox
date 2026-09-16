package eval

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyStreamsBeforeEOFAndCancelsUpstream(t *testing.T) {
	observed := make(chan *http.Request, 1)
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- request.Clone(context.Background())
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Connection", "X-Response-Hop")
		writer.Header().Set("X-Response-Hop", "remove")
		_, _ = io.WriteString(writer, "data: first\n\n")
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	gateway, err := startGateway(1, nil, upstream.URL+"/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.BaseURL()+"/chat/completions?q=a", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test")
	request.Header.Set("Connection", "X-Hop")
	request.Header.Set("X-Hop", "remove")
	request.Header.Set("X-Forwarded-For", "attacker")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("incremental SSE = %q, %v", line, err)
	}
	if response.Header.Get("X-Response-Hop") != "" {
		t.Error("hop response header forwarded")
	}
	got := <-observed
	if got.URL.Path != "/api/v1/chat/completions" || got.URL.RawQuery != "q=a" || got.Header.Get("Authorization") != "Bearer test" ||
		got.Header.Get("X-Hop") != "" || got.Header.Get("X-Forwarded-For") != "" {
		t.Fatalf("proxy rewrite = %s, %#v", got.URL, got.Header)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not cancelled")
	}
	if attempts, exceeded := gateway.Counts(); attempts != 1 || exceeded {
		t.Fatalf("budget = %d, %v", attempts, exceeded)
	}
}

func TestProxyPreservesUpstreamErrorsAndDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Location", "https://example.invalid/credential-target")
				writer.WriteHeader(status)
				_, _ = io.WriteString(writer, "upstream failure")
			}))
			defer upstream.Close()
			gateway, err := startGateway(1, nil, upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			response, err := client.Get(gateway.BaseURL() + "/chat/completions")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != status || string(body) != "upstream failure" {
				t.Fatalf("upstream error = %d, %q, %v", response.StatusCode, body, err)
			}
		})
	}
}
