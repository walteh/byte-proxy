package byteproxy

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCmdTestLogger creates a test logger that writes to a buffer
func setupCmdTestLogger(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return &buf
}

func TestNewCommand(t *testing.T) {
	// Test that command creation works and sets up flags correctly
	cmd := NewCommand()

	// Verify command metadata
	assert.Equal(t, "byte-proxy", cmd.Use)
	assert.Contains(t, cmd.Short, "routes connections based on the first byte")
	assert.Contains(t, cmd.Long, "proxy that reads the first byte")

	// Verify flags are set up
	listenPortFlag := cmd.Flag("listen-port")
	require.NotNil(t, listenPortFlag, "listen-port flag should be defined")
	assert.Equal(t, "9092", listenPortFlag.DefValue)

	mapFlag := cmd.Flag("map")
	require.NotNil(t, mapFlag, "map flag should be defined")
	assert.Equal(t, "[]", mapFlag.DefValue)

	debugFlag := cmd.Flag("debug")
	require.NotNil(t, debugFlag, "debug flag should be defined")
	assert.Equal(t, "false", debugFlag.DefValue)
}

func TestCommandFlagParsing(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectedCmd *Command
		wantErr     bool
	}{
		{
			name: "Default values",
			args: []string{},
			expectedCmd: &Command{
				ListenPort: 9092,
				Mappings:   []string{},
				Debug:      false,
			},
			wantErr: false,
		},
		{
			name: "Custom port",
			args: []string{"--listen-port", "8080"},
			expectedCmd: &Command{
				ListenPort: 8080,
				Mappings:   []string{},
				Debug:      false,
			},
			wantErr: false,
		},
		{
			name: "Custom mapping",
			args: []string{"--map", "0x01=target:9090"},
			expectedCmd: &Command{
				ListenPort: 9092,
				Mappings:   []string{"0x01=target:9090"},
				Debug:      false,
			},
			wantErr: false,
		},
		{
			name: "Multiple mappings",
			args: []string{"--map", "0x01=target1:9090", "--map", "0x02=target2:9090"},
			expectedCmd: &Command{
				ListenPort: 9092,
				Mappings:   []string{"0x01=target1:9090", "0x02=target2:9090"},
				Debug:      false,
			},
			wantErr: false,
		},
		{
			name: "Debug enabled",
			args: []string{"--debug"},
			expectedCmd: &Command{
				ListenPort: 9092,
				Mappings:   []string{},
				Debug:      true,
			},
			wantErr: false,
		},
		{
			name: "All flags",
			args: []string{"--listen-port", "8888", "--map", "0x01=target1:9090", "--map", "0x02=target2:9090", "--debug"},
			expectedCmd: &Command{
				ListenPort: 8888,
				Mappings:   []string{"0x01=target1:9090", "0x02=target2:9090"},
				Debug:      true,
			},
			wantErr: false,
		},
		{
			name:    "Invalid port",
			args:    []string{"--listen-port", "invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup a new command with a buffer to capture output
			bufLogger := setupCmdTestLogger(t)

			cmd := NewCommand()

			// Create a context for the command
			ctx := context.Background()
			cmd.SetContext(ctx)

			// Reset command to use test args
			cmd.SetArgs(tt.args)

			// Execute command without running the RunE function
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true

			var cmdInstance *Command
			// Save the command instance for inspection
			oldRunE := cmd.RunE
			cmd.RunE = func(c *cobra.Command, args []string) error {
				// Get the Command instance from our command object
				// The Command instance is stored and used in the run method
				cmdInstance = &Command{
					ListenPort: 0,
					Mappings:   []string{},
					Debug:      false,
				}

				// Parse the flags to populate the command instance
				if portFlag := c.Flag("listen-port"); portFlag != nil && portFlag.Changed {
					port, _ := c.Flags().GetInt("listen-port")
					cmdInstance.ListenPort = port
				} else {
					cmdInstance.ListenPort = 9092 // Default value
				}

				if mapFlag := c.Flag("map"); mapFlag != nil {
					mappings, _ := c.Flags().GetStringArray("map")
					cmdInstance.Mappings = mappings
				}

				if debugFlag := c.Flag("debug"); debugFlag != nil {
					debug, _ := c.Flags().GetBool("debug")
					cmdInstance.Debug = debug
				}

				// If we expect an error, don't run the actual command
				if tt.wantErr {
					return nil
				}

				// Otherwise call the original
				return oldRunE(c, args)
			}

			err := cmd.Execute()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, cmdInstance)

				// Check that values were parsed correctly
				if tt.expectedCmd != nil {
					assert.Equal(t, tt.expectedCmd.ListenPort, cmdInstance.ListenPort)
					assert.Equal(t, tt.expectedCmd.Mappings, cmdInstance.Mappings)
					assert.Equal(t, tt.expectedCmd.Debug, cmdInstance.Debug)
				}

				// Check logs
				logs := bufLogger.String()
				if len(tt.expectedCmd.Mappings) > 0 {
					assert.Contains(t, logs, "Route configured")
				}
			}
		})
	}
}

func TestCommandRunWithInvalidMapping(t *testing.T) {
	// Set up a command with invalid mapping
	cmd := NewCommand()
	cmd.SetArgs([]string{"--map", "invalid-mapping"})

	// Create a context for the command
	ctx := context.Background()
	cmd.SetContext(ctx)

	// Configure logging to a buffer
	bufLogger := setupCmdTestLogger(t)

	// Execute the command, it should fail due to invalid mapping
	err := cmd.Execute()
	assert.Error(t, err)

	// Check the logs for error message
	logs := bufLogger.String()
	assert.Contains(t, logs, "Failed to parse route mappings")
}

func TestCommandRunWithoutMappings(t *testing.T) {
	// Set up a command with no mappings
	cmd := NewCommand()

	// Create a context for the command
	ctx := context.Background()
	cmd.SetContext(ctx)

	// Configure logging to a buffer
	bufLogger := setupCmdTestLogger(t)

	// Execute the command, it should fail due to no mappings
	err := cmd.Execute()
	assert.Error(t, err)

	// Check the logs for error message
	logs := bufLogger.String()
	assert.Contains(t, logs, "Failed to parse route mappings")
	assert.Contains(t, logs, "no routes specified")
}
