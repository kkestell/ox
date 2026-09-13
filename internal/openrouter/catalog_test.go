package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestCatalogDecodingAndCapabilityLookup(t *testing.T) {
	var envelope modelEnvelope
	err := json.Unmarshal([]byte(`{"data":[
		{
			"id":"top/level",
			"name":"Top Level",
			"context_length":131072,
			"architecture":{
				"input_modalities":["text","image"],
				"output_modalities":["text"]
			},
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
		model.Name != "Top Level" ||
		strings.Join(model.Architecture.InputModalities, ",") != "text,image" ||
		strings.Join(model.Architecture.OutputModalities, ",") != "text" ||
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
	client.CachePath = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ContextWindow("fresh/model") != 4096 || requests.Load() != 1 {
		t.Fatalf("catalog = %#v, requests = %d", catalog, requests.Load())
	}
	reloaded, _, err := readCatalogCache(path)
	if err != nil || reloaded.ContextWindow("fresh/model") != 4096 {
		t.Fatalf("rewritten cache = %#v, %v", reloaded, err)
	}
}

// TestCatalogServesAFreshCacheWithoutARequest covers the ordinary activation:
// a cache written recently answers immediately and the provider is not asked.
func TestCatalogServesAFreshCacheWithoutARequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := writeCatalogCache(path, []Model{{
		ID:            "cached/model",
		ContextLength: 1024,
	}}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model","context_length":2048}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.CachePath = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ContextWindow("cached/model") != 1024 {
		t.Fatalf("cached catalog = %#v", catalog)
	}
	if second, err := client.Catalog(t.Context()); err != nil || second != catalog {
		t.Fatalf("second catalog = %#v, %v", second, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want a fresh cache to need none", requests.Load())
	}
}

// TestCatalogRefetchesAStaleCache covers the other side of the freshness rule,
// including the fallback that keeps an unreachable provider from leaving a
// session with no catalog at all.
func TestCatalogRefetchesAStaleCache(t *testing.T) {
	stale := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		if err := writeCatalogCache(path, []Model{{ID: "cached/model", ContextLength: 1024}}); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-catalogMaxAge - time.Minute)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("refetches and rewrites", func(t *testing.T) {
		path := stale(t)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model","context_length":2048}]}`)
		}))
		t.Cleanup(server.Close)

		client := testClient(server.URL)
		client.CachePath = path
		catalog, err := client.Catalog(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if catalog.ContextWindow("fresh/model") != 2048 || requests.Load() != 1 {
			t.Fatalf("catalog = %#v after %d requests", catalog, requests.Load())
		}
		rewritten, age, err := readCatalogCache(path)
		if err != nil || rewritten.ContextWindow("fresh/model") != 2048 || age > time.Minute {
			t.Fatalf("rewritten cache = %#v, age %v, %v", rewritten, age, err)
		}
	})

	t.Run("falls back to the stale entries", func(t *testing.T) {
		path := stale(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		t.Cleanup(server.Close)

		client := testClient(server.URL)
		client.CachePath = path
		catalog, err := client.Catalog(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if catalog.ContextWindow("cached/model") != 1024 {
			t.Fatalf("stale fallback = %#v", catalog)
		}
	})
}

// TestCatalogExpiresInMemory covers the freshness rule a long-running process
// depends on. The catalog held in memory ages out with the catalog itself, so
// a process that runs for days sees current context windows and parameters,
// while a catalog that is already stale is retried on an interval rather than
// on every request.
func TestCatalogExpiresInMemory(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name string
		age  time.Duration
		want time.Duration
	}{
		{name: "freshly fetched", age: 0, want: catalogMaxAge},
		{name: "partly aged cache", age: catalogMaxAge - time.Hour, want: time.Hour},
		{
			name: "nearly expired cache",
			age:  catalogMaxAge - time.Second, want: catalogRetryInterval,
		},
		{
			name: "stale fallback",
			age:  catalogMaxAge + time.Hour, want: catalogRetryInterval,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := catalogExpiry(now, test.age).Sub(now); got != test.want {
				t.Fatalf("lifetime = %v, want %v", got, test.want)
			}
		})
	}

	// An unreachable provider must not cost a fetch per request, and must not
	// fail a session the process already holds a catalog for.
	t.Run("serves the held catalog when the refresh fails", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		t.Cleanup(server.Close)

		client := testClient(server.URL)
		client.catalog = newCatalog([]Model{{ID: "held/model", ContextLength: 1024}})
		client.catalogExpires = time.Now().Add(-time.Second)

		catalog, err := client.Catalog(t.Context())
		if err != nil || catalog.ContextWindow("held/model") != 1024 {
			t.Fatalf("catalog = %#v, %v", catalog, err)
		}
		if requests.Load() != 1 {
			t.Fatalf("requests = %d, want 1", requests.Load())
		}
		again, err := client.Catalog(t.Context())
		if err != nil || again != catalog {
			t.Fatalf("second catalog = %#v, %v", again, err)
		}
		if requests.Load() != 1 {
			t.Fatalf("requests after a bounded retry = %d, want 1", requests.Load())
		}
	})

	// A caller that gave up gets its own error, not a catalog it no longer
	// wants.
	t.Run("propagates cancellation instead of serving the held catalog", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		}))
		t.Cleanup(server.Close)

		client := testClient(server.URL)
		client.catalog = newCatalog([]Model{{ID: "held/model", ContextLength: 1024}})
		client.catalogExpires = time.Now().Add(-time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if _, err := client.Catalog(ctx); err == nil {
			t.Fatal("a cancelled load served the held catalog")
		}
	})

	t.Run("reloads once the memory copy expires", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models.json")
		if err := writeCatalogCache(path, []Model{
			{ID: "reloaded/model", ContextLength: 4096},
		}); err != nil {
			t.Fatal(err)
		}
		client := testClient("")
		client.CachePath = path
		client.catalog = newCatalog([]Model{{ID: "memoized/model", ContextLength: 1024}})
		client.catalogExpires = time.Now().Add(-time.Second)

		catalog, err := client.Catalog(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if catalog.ContextWindow("reloaded/model") != 4096 {
			t.Fatalf("expired memory catalog was served: %#v", catalog)
		}
	})
}

// TestCatalogRejectsACacheWithNoModels covers a cache that parses but answers
// nothing. Accepting it would memoize an empty catalog for the process.
func TestCatalogRejectsACacheWithNoModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"data":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCatalogCache(path); err == nil ||
		!strings.Contains(err.Error(), "no models") {
		t.Fatalf("empty cache error = %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"fresh/model","context_length":2048}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.CachePath = path
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ContextWindow("fresh/model") != 2048 || requests.Load() != 1 {
		t.Fatalf("catalog = %#v after %d requests", catalog, requests.Load())
	}
}

// TestCatalogRefusesAnOversizedResponse keeps a hostile or broken endpoint from
// being read into memory without a bound.
func TestCatalogRefusesAnOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"huge/model","name":"`)
		filler := strings.Repeat("x", 1<<20)
		for written := 0; written <= maxCatalogBytes; written += len(filler) {
			if _, err := io.WriteString(w, filler); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}]}`)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.CachePath = filepath.Join(t.TempDir(), "models.json")
	if _, err := client.Catalog(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
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
	client.CachePath = filepath.Join(t.TempDir(), "models.json")
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
	client.CachePath = filepath.Join(t.TempDir(), "models.json")
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
		ID: "stable/model",
		Architecture: Architecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
		},
		SupportedParameters: []string{"tools"},
		Reasoning: &ModelReasoning{
			SupportedEfforts: []string{"high"},
		},
	}})
	models := catalog.Models()
	models[0].ID = "changed/model"
	models[0].SupportedParameters[0] = "changed"
	models[0].Architecture.InputModalities[0] = "changed"
	models[0].Architecture.OutputModalities[0] = "changed"
	models[0].Reasoning.SupportedEfforts[0] = "low"

	model, ok := catalog.Model("stable/model")
	if !ok ||
		model.ID != "stable/model" ||
		model.SupportedParameters[0] != "tools" ||
		model.Architecture.InputModalities[0] != "text" ||
		model.Architecture.OutputModalities[0] != "text" ||
		model.Reasoning.SupportedEfforts[0] != "high" {
		t.Fatalf("catalog was mutated through Models: %#v, %v", model, ok)
	}
	model.ID = "changed/again"
	if _, ok := catalog.Model("stable/model"); !ok {
		t.Fatal("catalog was mutated through Model")
	}
}

func TestClientModelsAreSortedClones(t *testing.T) {
	client := &Client{
		catalog: newCatalog([]Model{
			{ID: "zeta/model", Name: "Zeta"},
			{ID: "alpha/model", Name: "Alpha"},
		}),
		catalogExpires: time.Now().Add(catalogMaxAge),
	}

	models, err := client.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := models[0].ID + "," + models[1].ID; got != "alpha/model,zeta/model" {
		t.Fatalf("models = %q", got)
	}
	models[0].Name = "Changed"
	again, err := client.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Name != "Alpha" {
		t.Fatalf("model name = %q", again[0].Name)
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

	catalog, _, err := readCatalogCache(path)
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
	client.CachePath = filepath.Join(t.TempDir(), "models.json")
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
	client.CachePath = filepath.Join(t.TempDir(), "models.json")
	client.retryWait = noWait
	catalog, err := client.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Model("fresh/model"); !ok || requests.Load() != 2 {
		t.Fatalf("catalog = %#v, requests = %d", catalog, requests.Load())
	}
}
