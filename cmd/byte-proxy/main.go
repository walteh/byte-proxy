package byteproxy

import (
	"fmt"
	"os"
)

// Main is the entry point for the byte-proxy command when used as a library
func Main() {
	cmd := NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
