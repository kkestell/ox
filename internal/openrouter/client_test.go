package openrouter

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestVerifyCredential(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		rejected bool
		wantErr  string
	}{
		{name: "accepted", status: http.StatusOK, body: `{"data":{}}`},
		{
			name: "unauthorized", status: http.StatusUnauthorized,
			body:     `{"error":{"message":"User not found.","code":401}}`,
			rejected: true, wantErr: "User not found.",
		},
		{
			name: "forbidden", status: http.StatusForbidden,
			body:     `{"error":{"message":"Key disabled.","code":403}}`,
			rejected: true, wantErr: "Key disabled.",
		},
		{
			name: "provider failure", status: http.StatusServiceUnavailable,
			body: "try later", wantErr: "503 Service Unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var method, path, authorization string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				method = request.Method
				path = request.URL.Path
				authorization = request.Header.Get("Authorization")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)

			client := &Client{BaseURL: server.URL}
			err := client.VerifyCredential(t.Context(), "credential")
			if (err != nil) != (test.wantErr != "") {
				t.Fatalf("VerifyCredential error = %v", err)
			}
			if test.wantErr != "" && !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("VerifyCredential error = %v, want %q", err, test.wantErr)
			}
			if errors.Is(err, ErrCredentialRejected) != test.rejected {
				t.Fatalf("errors.Is(ErrCredentialRejected) = %t, want %t", errors.Is(err, ErrCredentialRejected), test.rejected)
			}
			if method != http.MethodGet || path != "/key" {
				t.Errorf("request = %s %s, want GET /key", method, path)
			}
			if authorization != "Bearer credential" {
				t.Errorf("Authorization = %q", authorization)
			}
		})
	}
}

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
