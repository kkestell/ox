// ur is Ox's built-in harness, which oxd starts for each running Ur session
// and drives over stdin and stdout.
package main

import (
	"os"

	"github.com/kkestell/ox/internal/ur/engine"
)

func main() {
	os.Exit(engine.Main(os.Args[1:]))
}
