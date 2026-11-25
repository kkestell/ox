//go:build !screenshots

package main

import "github.com/kkestell/ox/internal/agent"

// playScene leaves the harnesses alone outside a screenshots build.
func playScene(map[string]agent.Harness) {}
