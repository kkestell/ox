// Command field-sync uploads pending survey records to the records API.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"example.com/field-sync/internal/sync"
)

func main() {
	api := flag.String("api", "https://records.example.com", "records API base URL")
	dir := flag.String("dir", "records", "directory of pending records")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := sync.NewClient(*api)
	if err := client.UploadAll(ctx, *dir); err != nil {
		log.Fatal(err)
	}
}
