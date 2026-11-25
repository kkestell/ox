//go:build screenshots

package main

import (
	"flag"
	"log"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/fake"
)

var scenePath = flag.String("scene", "", "play this scene file in place of Claude and Ur")

// playScene replaces Claude and Ur with agents that play the scene, for the
// README's screenshots.
func playScene(harnesses map[string]agent.Harness) {
	if *scenePath == "" {
		log.Fatal("-scene is required in a screenshots build")
	}
	scene, err := fake.LoadScene(*scenePath)
	if err != nil {
		log.Fatal(err)
	}
	harnesses["claude"] = fake.Scripted(scene)
	harnesses["ur"] = fake.Scripted(scene)
}
