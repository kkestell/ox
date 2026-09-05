package eval

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

type providerGateway struct {
	server   *httptest.Server
	upstream http.Handler
	limit    int

	mu       sync.Mutex
	attempts int
	exceeded bool
}

func startGateway(limit int, upstream http.Handler, upstreamURL string) (*providerGateway, error) {
	if upstream == nil {
		if upstreamURL == "" {
			return nil, errors.New("provider endpoint is required")
		}
		proxy, err := proxyHandler(upstreamURL)
		if err != nil {
			return nil, err
		}
		upstream = proxy
	}
	gateway := &providerGateway{upstream: upstream, limit: limit}
	gateway.server = httptest.NewServer(http.HandlerFunc(gateway.serveHTTP))
	return gateway, nil
}

func (g *providerGateway) Close() {
	g.server.CloseClientConnections()
	g.server.Close()
}

func (g *providerGateway) BaseURL() string { return g.server.URL + "/api/v1" }

func (g *providerGateway) Counts() (attempts int, exceeded bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.attempts, g.exceeded
}

func (g *providerGateway) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if strings.HasSuffix(request.URL.Path, "/chat/completions") {
		g.mu.Lock()
		g.attempts++
		allowed := g.attempts <= g.limit
		if !allowed {
			g.exceeded = true
		}
		g.mu.Unlock()
		if !allowed {
			http.Error(writer, `{"error":{"message":"evaluation provider request budget exceeded"}}`, http.StatusBadRequest)
			return
		}
	}
	g.upstream.ServeHTTP(writer, request)
}

func proxyHandler(rawURL string) (http.Handler, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid provider endpoint %q", rawURL)
	}
	client := &http.Client{}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if strings.HasSuffix(target.Path, "/api/v1") {
			path = strings.TrimPrefix(path, "/api/v1")
		}
		destination := *target
		destination.Path = strings.TrimSuffix(target.Path, "/") + path
		destination.RawQuery = request.URL.RawQuery
		upstream, err := http.NewRequestWithContext(request.Context(), request.Method, destination.String(), request.Body)
		if err != nil {
			http.Error(writer, "build provider request", http.StatusBadGateway)
			return
		}
		upstream.Header = request.Header.Clone()
		response, err := client.Do(upstream)
		if err != nil {
			http.Error(writer, "provider unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}), nil
}
