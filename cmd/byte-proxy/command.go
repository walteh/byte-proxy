package byteproxy

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/walteh/byte-proxy/pkg/bproxy"
)

// Command represents the byte-proxy command
type Command struct {
	ListenPort int
	Mappings   []string
	Debug      bool
}

// NewCommand creates a new byte-proxy command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "byte-proxy",
		Short: "Proxy that routes connections based on the first byte of each request",
		Long: `A TCP proxy that reads the first byte of each connection and forwards it to
a specific destination based on a routing table. The first byte is stripped before forwarding.

Example:
  byte-proxy --listen-port 9092 --map "0x01=broker1:9094" --map "0x02=broker2:9094"

This routes connections with first byte 0x01 to broker1:9094 and 0x02 to broker2:9094.`,
	}

	cmda := &Command{}

	cmd.Flags().IntVar(&cmda.ListenPort, "listen-port", 9092, "Port to listen on")
	cmd.Flags().StringArrayVar(&cmda.Mappings, "map", []string{}, "Mapping in the format '0xXX=host:port' (e.g., '0x01=broker1:9094')")
	cmd.Flags().BoolVar(&cmda.Debug, "debug", false, "Enable debug logging")

	cmd.RunE = cmda.run

	return cmd
}

func (me *Command) run(cmd *cobra.Command, args []string) error {
	// Get context from cobra command - never use context.Background()
	ctx := cmd.Context()

	// Setup logger - we need to set up the logger here to configure it based on debug flag
	var logLevel slog.Level
	if me.Debug {
		logLevel = slog.LevelDebug
	} else {
		logLevel = slog.LevelInfo
	}

	// Create a level variable we can adjust
	levelVar := new(slog.LevelVar)
	levelVar.Set(logLevel)

	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelVar,
	})
	logger := slog.New(logHandler)

	// Set the logger as the default logger for the entire program
	slog.SetDefault(logger)

	// Create the proxy
	proxy := bproxy.New(me.ListenPort, me.Debug)

	// Parse mappings
	err := proxy.ParseHexRoutes(me.Mappings)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to parse route mappings", "error", err)
		return err
	}

	// Setup signal handling for graceful shutdown
	proxy.SetupCleanupOnSignals()

	// Start the proxy
	slog.InfoContext(ctx, "Starting byte-proxy", "port", me.ListenPort, "routes", len(me.Mappings))
	return proxy.Start(ctx)
}
