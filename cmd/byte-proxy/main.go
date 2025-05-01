package byteproxy

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// CommandCreator is a function type for creating the root command
// This allows us to swap it out in tests
var CommandCreator = NewCommand

// Main is the entry point for the byte-proxy command when used as a library
func Main() {
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
	cmd := CommandCreator()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
