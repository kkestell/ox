//go:build oxe2e

package tools

import (
	"context"
	"net"
	"net/netip"
	"os"
)

// The e2e binary maps one public-looking fixture host to a loopback listener.
// This file is excluded from production builds, where every lookup and dial
// uses the public-address policy in web_fetch.go unchanged.
func init() {
	host := os.Getenv("OX_E2E_WEB_FETCH_HOST")
	target := os.Getenv("OX_E2E_WEB_FETCH_TARGET")
	if host == "" || target == "" {
		return
	}
	production := defaultWebFetchNetwork
	defaultWebFetchNetwork = webFetchNetwork{
		lookup: func(ctx context.Context, candidate string) ([]netip.Addr, error) {
			if candidate == host {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			return production.lookup(ctx, candidate)
		},
		dial: func(ctx context.Context, protocol, address string) (net.Conn, error) {
			requested, _, err := net.SplitHostPort(address)
			if err == nil && requested == "93.184.216.34" {
				return production.dial(ctx, protocol, target)
			}
			return production.dial(ctx, protocol, address)
		},
	}
}
