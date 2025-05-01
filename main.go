package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	byteproxy "github.com/walteh/byte-proxy/cmd/byte-proxy"
)

func main() {
	// Create a context that is canceled when SIGTERM or SIGINT is received
	ctx, cancel := context.WithCancel(context.TODO())

	// Create a signal channel to handle termination
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Handle signals in a separate goroutine
	go func() {
		<-sigChan
		cancel()
	}()

	// Run the command with the context
	cmd := byteproxy.NewCommand()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
