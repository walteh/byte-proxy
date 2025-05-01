package byteproxy

import (
	"context"
	"os"

	"github.com/rs/zerolog"
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
	// Setup logger
	logLevel := zerolog.InfoLevel
	if me.Debug {
		logLevel = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(logLevel)
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	ctx := logger.WithContext(context.Background())

	// Create the proxy
	proxy := bproxy.New(me.ListenPort, me.Debug)

	// Parse mappings
	err := proxy.ParseHexRoutes(me.Mappings)
	if err != nil {
		return err
	}

	// Setup signal handling for graceful shutdown
	proxy.SetupCleanupOnSignals()

	// Start the proxy
	return proxy.Start(ctx)
}
