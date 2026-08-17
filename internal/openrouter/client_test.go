package openrouter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// The accessor is read when a request is built, not when the client is
// constructed, which is what lets a credential change reach the sessions that
// already exist.
func TestClientResolvesAPIKeyForEachRequest(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	key := "first"
	client := &Client{
		APIKey:  func() string { return key },
		BaseURL: server.URL,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := Request{Model: "test/model", Messages: []Message{}}
	if _, err := client.Stream(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	key = "second"
	if _, err := client.Stream(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bearer first", "Bearer second"}
	if !slices.Equal(authorizations, want) {
		t.Fatalf("Authorization headers = %#v, want %#v", authorizations, want)
	}
}

func TestClientPanicsWithoutAPIKeyAccessor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("apiKey did not panic")
		}
	}()
	(&Client{}).apiKey()
}
