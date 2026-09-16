package eval

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
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
	return &httputil.ReverseProxy{
		Rewrite: func(proxy *httputil.ProxyRequest) {
			path := proxy.In.URL.Path
			if strings.HasSuffix(target.Path, "/api/v1") {
				path = strings.TrimPrefix(path, "/api/v1")
			}
			proxy.SetURL(target)
			proxy.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + path
			proxy.Out.URL.RawPath = ""
			proxy.Out.URL.RawQuery = proxy.In.URL.RawQuery
		},
		FlushInterval: -1,
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(writer, "provider unavailable", http.StatusBadGateway)
		},
	}, nil
}
