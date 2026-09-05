package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

const (
	webFetchTimeout     = 30 * time.Second
	webFetchBodyBytes   = 2 << 20
	webFetchInlineBytes = 64 << 10
	webFetchUserAgent   = "Ox/1 web_fetch"
)

const webFetchDescription = "Fetch a public HTTP(S) URL as untrusted source text. " +
	"HTML, plain text, and JSON are supported. Redirects, time, and response size are bounded; " +
	"larger accepted output spills to a readable session file. Requires permission."

const webFetchSchema = `{
	"type": "object",
	"properties": {
		"url": {"type": "string", "description": "Absolute public HTTP or HTTPS URL."}
	},
	"required": ["url"],
	"additionalProperties": false
}`

type webFetchArguments struct {
	URL *string `json:"url"`
}

type webFetchNetwork struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

var defaultWebFetchNetwork = webFetchNetwork{
	lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	},
	dial: (&net.Dialer{}).DialContext,
}

var blockedWebPrefixes = mustWebPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

func mustWebPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, len(values))
	for index, value := range values {
		prefixes[index] = netip.MustParsePrefix(value)
	}
	return prefixes
}

func executeWebFetch(ctx context.Context, invocation agent.Invocation) (string, error) {
	return executeWebFetchWith(ctx, invocation, defaultWebFetchNetwork)
}

func executeWebFetchWith(
	ctx context.Context,
	invocation agent.Invocation,
	network webFetchNetwork,
) (string, error) {
	var arguments webFetchArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	rawURL, err := requireString("url", arguments.URL)
	if err != nil {
		return "", err
	}
	requested, err := parseWebURL(rawURL)
	if err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            network.dialPublic,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  webFetchTimeout,
		MaxResponseHeaderBytes: 1 << 20,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("web fetch redirect limit of 5 exceeded")
			}
			return validateWebURL(request.URL)
		},
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, requested.String(), nil)
	if err != nil {
		return "", errors.New("invalid web fetch URL")
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, application/json")

	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web fetch failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("web fetch returned HTTP %d", response.StatusCode)
	}
	if encoding := strings.TrimSpace(response.Header.Get("Content-Encoding")); encoding != "" &&
		!strings.EqualFold(encoding, "identity") {
		return "", fmt.Errorf("web fetch does not support content encoding %q", encoding)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, webFetchBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read web response: %w", err)
	}
	if len(body) > webFetchBodyBytes {
		return "", fmt.Errorf("web response exceeds the %d-byte decoded body limit", webFetchBodyBytes)
	}
	mediaType, content, err := webFetchContent(response.Header.Get("Content-Type"), body)
	if err != nil {
		return "", err
	}
	finalURL := requested.String()
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	output := fmt.Sprintf(
		"Requested URL: %s\nFinal URL: %s\nContent-Type: %s\n\n<untrusted_web_source>\n%s\n</untrusted_web_source>",
		requested.String(), finalURL, mediaType, content,
	)
	rendered, err := workspace.RenderText(
		invocation.SpillDir, "web-fetch", invocation.CallID, output, webFetchInlineBytes,
	)
	if err != nil {
		return "", err
	}
	if rendered.Spilled != "" && invocation.ReportSpill != nil {
		invocation.ReportSpill(rendered.Spilled)
	}
	return rendered.Content, nil
}

func parseWebURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("`url` must be a valid absolute URL")
	}
	if err := validateWebURL(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateWebURL(parsed *url.URL) error {
	if parsed == nil || parsed.Hostname() == "" || !parsed.IsAbs() {
		return errors.New("`url` must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("`url` must use HTTP or HTTPS")
	}
	if parsed.User != nil {
		return errors.New("`url` must not contain credentials")
	}
	return nil
}

func (network webFetchNetwork) dialPublic(
	ctx context.Context,
	protocol string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("web destination has an invalid network address")
	}
	addresses, err := network.resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve web destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("web destination resolved to no addresses")
	}
	for _, candidate := range addresses {
		if !publicWebAddress(candidate) {
			return nil, fmt.Errorf("web destination resolved to nonpublic address %s", candidate)
		}
	}
	var failures []error
	for _, candidate := range addresses {
		connection, err := network.dial(ctx, protocol, net.JoinHostPort(candidate.String(), port))
		if err == nil {
			return connection, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("connect to web destination: %w", errors.Join(failures...))
}

func (network webFetchNetwork) resolve(
	ctx context.Context,
	host string,
) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal}, nil
	}
	if network.lookup == nil {
		return nil, errors.New("web resolver is unavailable")
	}
	return network.lookup(ctx, host)
}

func publicWebAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range blockedWebPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func webFetchContent(header string, body []byte) (string, string, error) {
	mediaType := ""
	parameters := map[string]string(nil)
	var err error
	if header != "" {
		mediaType, parameters, err = mime.ParseMediaType(header)
		if err != nil {
			return "", "", errors.New("web response has an invalid Content-Type")
		}
	} else {
		mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	mediaType = strings.ToLower(mediaType)
	if charset := strings.ToLower(parameters["charset"]); charset != "" &&
		charset != "utf-8" && charset != "us-ascii" {
		return "", "", fmt.Errorf("web response uses unsupported charset %q", charset)
	}
	if !utf8.Valid(body) {
		return "", "", errors.New("web response text is not valid UTF-8")
	}

	switch mediaType {
	case "text/html", "application/xhtml+xml":
		converted, err := htmltomarkdown.ConvertReader(bytes.NewReader(body))
		if err != nil {
			return "", "", fmt.Errorf("extract web page text: %w", err)
		}
		return mediaType, strings.TrimSpace(string(converted)), nil
	case "text/plain":
		return mediaType, strings.TrimSpace(string(body)), nil
	case "application/json":
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, bytes.TrimSpace(body), "", "  "); err != nil {
			return "", "", errors.New("web response contains invalid JSON")
		}
		return mediaType, formatted.String(), nil
	default:
		return "", "", fmt.Errorf("web response Content-Type %q is not supported", mediaType)
	}
}

func webFetchTitle(arguments json.RawMessage) string {
	var input struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(arguments, &input) != nil || input.URL == "" {
		return "Fetch web page"
	}
	return "Fetch " + input.URL
}
