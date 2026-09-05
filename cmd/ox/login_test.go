package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

func TestLoginVerifiesAndStoresCredential(t *testing.T) {
	keyring.MockInit()
	const key = "login-key-that-must-stay-secret"
	var method, path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		path = request.URL.Path
		authorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	input := pipeInput(t, "  "+key+"  \n")
	store := credentials.NewStore(commandTestLogger(), "", false)
	client := &openrouter.Client{BaseURL: server.URL}
	var output strings.Builder
	if err := login(t.Context(), input, &output, store, client); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || path != "/auth/key" {
		t.Errorf("request = %s %s, want GET /auth/key", method, path)
	}
	if authorization != "Bearer "+key {
		t.Errorf("Authorization = %q", authorization)
	}
	if store.Key() != key || store.Source() != credentials.SourceKeyring {
		t.Errorf("stored credential = %q from %q", store.Key(), store.Source())
	}
	if got := output.String(); !strings.Contains(got, "OS keyring") || strings.Contains(got, key) {
		t.Errorf("output = %q", got)
	}
}

func TestLoginRefusesInvalidInputWithoutChangingKeyring(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   string
		status  int
		body    string
		wantErr string
	}{
		{name: "blank", input: "  \n", status: http.StatusOK, wantErr: "empty"},
		{
			name: "rejected", input: "rejected\n", status: http.StatusUnauthorized,
			body: `{"error":{"message":"User not found."}}`, wantErr: "User not found.",
		},
		{name: "provider failure", input: "key\n", status: http.StatusServiceUnavailable, body: "later", wantErr: "503"},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyring.MockInit()
			store := credentials.NewStore(commandTestLogger(), "", false)
			if err := store.Set("existing"); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests++
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)

			err := login(
				t.Context(), pipeInput(t, test.input), io.Discard, store,
				&openrouter.Client{BaseURL: server.URL},
			)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("login error = %v, want %q", err, test.wantErr)
			}
			if test.name == "blank" && requests != 0 {
				t.Errorf("blank credential made %d provider requests", requests)
			}
			if store.Key() != "existing" {
				t.Errorf("keyring credential changed to %q", store.Key())
			}
		})
	}
}

func TestLoginRefusesUnreachableProvider(t *testing.T) {
	keyring.MockInit()
	store := credentials.NewStore(commandTestLogger(), "", false)
	if err := store.Set("existing"); err != nil {
		t.Fatal(err)
	}
	err := login(
		t.Context(), pipeInput(t, "key\n"), io.Discard, store,
		// Nothing listens on port 1, so the request is refused rather than sent.
		&openrouter.Client{BaseURL: "http://127.0.0.1:1/api/v1"},
	)
	if err == nil || !strings.Contains(err.Error(), "verify OpenRouter API key") {
		t.Fatalf("login error = %v", err)
	}
	if store.Key() != "existing" {
		t.Errorf("keyring credential changed to %q", store.Key())
	}
}

func TestLoginRefusesBeforeReadingWhenKeyringIsDisabled(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring must not be touched"))
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })

	store := credentials.NewStore(commandTestLogger(), "", true)
	err = login(t.Context(), reader, io.Discard, store, &openrouter.Client{})
	if err == nil || err.Error() != credentials.KeyringDisabledMessage {
		t.Fatalf("login error = %v", err)
	}
}

func TestLoginRefusesCredentialFileBeforeReading(t *testing.T) {
	keyring.MockInit()
	store := credentials.NewStore(commandTestLogger(), "file-key", false)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	if err := login(t.Context(), reader, io.Discard, store, &openrouter.Client{}); !errors.Is(err, credentials.ErrCredentialFileImmutable) {
		t.Fatalf("login error = %v", err)
	}
}

func pipeInput(t *testing.T, value string) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, value); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func commandTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
