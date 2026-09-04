package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestCatalogDecodingAndCapabilityLookup(t *testing.T) {
	var envelope modelEnvelope
	err := json.Unmarshal([]byte(`{"data":[
		{
			"id":"top/level",
			"context_length":131072,
			"top_provider":{"context_length":65536,"max_completion_tokens":8192},
			"pricing":{"prompt":"0.1","input_cache_read":"0.01"},
			"supported_parameters":["tools","reasoning"],
			"reasoning":{
				"mandatory":true,
				"default_enabled":true,
				"supported_efforts":["max","high","low"],
				"default_effort":"high",
				"supports_max_tokens":true
			}
		},
		{
			"id":"provider/fallback",
			"top_provider":{"context_length":32768},
			"reasoning":{}
		},
		{"id":"no/reasoning"}
	]}`), &envelope)
	if err != nil {
		t.Fatal(err)
	}
	catalog := newCatalog(envelope.Data)
	if catalog.ContextWindow("top/level") != 131072 ||
		catalog.ContextWindow("provider/fallback") != 32768 ||
		catalog.ContextWindow("missing/model") != 0 {
		t.Fatalf(
			"context windows = %d, %d, %d",
			catalog.ContextWindow("top/level"),
			catalog.ContextWindow("provider/fallback"),
			catalog.ContextWindow("missing/model"),
		)
	}
	reasoning := catalog.Reasoning("top/level")
	if reasoning == nil ||
		!reasoning.Mandatory ||
		!reasoning.DefaultEnabled ||
		reasoning.DefaultEffort != "high" ||
		!reasoning.SupportsMaxTokens ||
		strings.Join(reasoning.SupportedEfforts, ",") != "max,high,low" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	if catalog.Reasoning("provider/fallback") == nil {
		t.Fatal("empty reasoning object was not distinguishable")
	}
	if catalog.Reasoning("no/reasoning") != nil {
		t.Fatal("absent reasoning object was not preserved")
	}
	model, ok := catalog.Model("top/level")
	if !ok ||
		model.Pricing.InputCacheRead != "0.01" ||
		model.TopProvider.MaxCompletionTokens != 8192 {
		t.Fatalf("model = %#v, %v", model, ok)
	}
}

func TestCatalogResolvesAPIKeyPerRequest(t *testing.T) {
	key := "first"
	var authorization []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = append(authorization, request.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)
	client := testClient(server.URL)
	client.APIKey = func() string { return key }

	if _, _, _, err := client.fetchCatalogAttempt(t.Context()); err != nil {
		t.Fatal(err)
	}
	key = "second"
	if _, _, _, err := client.fetchCatalogAttempt(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(authorization, ",") != "Bearer first,Bearer second" {
		t.Fatalf("Authorization headers = %#v", authorization)
	}
}

func TestCatalogCachePathUsesXDGThenHome(t *testing.T) {
	if got := catalogCachePath("/var/cache/user", "/home/user"); got !=
		"/var/cache/user/ox/models.json" {
		t.Fatalf("XDG path = %q", got)
	}
	if got := catalogCachePath("relative", "/home/user"); got !=
		"/home/user/.cache/ox/models.json" {
		t.Fatalf("home path = %q", got)
	}
	if got := catalogCachePath("", "relative"); got != "" {
		t.Fatalf("unusable path = %q", got)
	}
}

func TestCatalogCorruptCacheIsMissAndIsRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model","context_length":4096}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ContextWindow("fresh/model") != 4096 || requests.Load() != 1 {
		t.Fatalf("catalog = %#v, requests = %d", catalog, requests.Load())
	}
	reloaded, err := readCatalogCache(path)
	if err != nil || reloaded.ContextWindow("fresh/model") != 4096 {
		t.Fatalf("rewritten cache = %#v, %v", reloaded, err)
	}
}

func TestCatalogCacheHitReturnsImmediatelyAndRefreshesExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := writeCatalogCache(path, []Model{{
		ID:            "cached/model",
		ContextLength: 1024,
	}}); err != nil {
		t.Fatal(err)
	}

	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(refreshStarted)
		}
		<-releaseRefresh
		_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model","context_length":2048}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ContextWindow("cached/model") != 1024 {
		t.Fatalf("cached catalog = %#v", catalog)
	}
	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not start")
	}
	if second, err := client.Catalog(t.Context()); err != nil || second != catalog {
		t.Fatalf("second catalog = %#v, %v", second, err)
	}
	close(releaseRefresh)

	deadline := time.Now().Add(2 * time.Second)
	for {
		refreshed, readErr := readCatalogCache(path)
		if readErr == nil && refreshed.ContextWindow("fresh/model") == 2048 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache was not refreshed: %#v, %v", refreshed, readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 1 {
		t.Fatalf("refresh requests = %d", requests.Load())
	}
}

func TestCatalogFailureDoesNotPoisonClient(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"recovered/model"}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = filepath.Join(t.TempDir(), "models.json")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Catalog(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("first catalog error = %v", err)
	}
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Model("recovered/model"); !ok {
		t.Fatalf("recovered catalog = %#v", catalog)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestConcurrentCatalogCallsShareOneLoad(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = io.WriteString(w, `{"data":[{"id":"shared/model"}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = filepath.Join(t.TempDir(), "models.json")
	results := make(chan *Catalog, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			catalog, err := client.Catalog(t.Context())
			results <- catalog
			errors <- err
		}()
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog load did not start")
	}
	close(release)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if catalog := <-results; catalog == nil {
			t.Fatal("catalog was nil")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestCatalogAccessorsDoNotExposeMutableState(t *testing.T) {
	catalog := newCatalog([]Model{{
		ID:                  "stable/model",
		SupportedParameters: []string{"tools"},
		Reasoning: &ModelReasoning{
			SupportedEfforts: []string{"high"},
		},
	}})
	models := catalog.Models()
	models[0].ID = "changed/model"
	models[0].SupportedParameters[0] = "changed"
	models[0].Reasoning.SupportedEfforts[0] = "low"

	model, ok := catalog.Model("stable/model")
	if !ok ||
		model.ID != "stable/model" ||
		model.SupportedParameters[0] != "tools" ||
		model.Reasoning.SupportedEfforts[0] != "high" {
		t.Fatalf("catalog was mutated through Models: %#v, %v", model, ok)
	}
	model.ID = "changed/again"
	if _, ok := catalog.Model("stable/model"); !ok {
		t.Fatal("catalog was mutated through Model")
	}
}

func TestCatalogWarmCacheSurvivesRefreshFailureAndLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := writeCatalogCache(path, []Model{{ID: "cached/model"}}); err != nil {
		t.Fatal(err)
	}
	var logs lockedBuffer
	requested := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requested)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	client.cachePathOverride = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Model("cached/model"); !ok {
		t.Fatal("warm cache was not served")
	}
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh was not requested")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "failed to refresh OpenRouter catalog cache") {
		if time.Now().After(deadline) {
			t.Fatalf("refresh failure was not logged:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConcurrentCatalogWritersLeaveReadableCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "models.json")
	var group sync.WaitGroup
	for index := range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = writeCatalogCache(path, []Model{{
				ID:            "model",
				ContextLength: index + 1,
			}})
		}()
	}
	group.Wait()

	catalog, err := readCatalogCache(path)
	if err != nil {
		t.Fatal(err)
	}
	window := catalog.ContextWindow("model")
	if window < 1 || window > 20 {
		t.Fatalf("context window = %d", window)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %#v", matches)
	}
}

func TestCatalogDoesNotRetryMalformedSuccessfulResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "not json")
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = filepath.Join(t.TempDir(), "models.json")
	client.retryWait = noWait
	if _, err := client.Catalog(t.Context()); err == nil {
		t.Fatal("malformed response succeeded")
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestCatalogRetriesServerError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model"}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.cachePathOverride = filepath.Join(t.TempDir(), "models.json")
	client.retryWait = noWait
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Model("fresh/model"); !ok || requests.Load() != 2 {
		t.Fatalf("catalog = %#v, requests = %d", catalog, requests.Load())
	}
}
