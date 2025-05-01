package bproxy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walteh/byte-proxy/pkg/bproxy"
)

// setupLogger creates a test logger that writes to a buffer
func setupLogger(t *testing.T) (*bytes.Buffer, context.Context) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()
	return &buf, ctx
}

// setupTestServers creates test TCP servers that echo back data prefixed with server ID
func setupTestServers(t *testing.T) ([]string, func()) {
	var servers []string
	var listeners []net.Listener
	var wg sync.WaitGroup

	// Create two test servers
	for i := 1; i <= 2; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		address := listener.Addr().String()
		servers = append(servers, address)
		listeners = append(listeners, listener)

		serverID := fmt.Sprintf("server%d", i)

		wg.Add(1)
		go func(l net.Listener, id string) {
			defer wg.Done()
			for {
				conn, err := l.Accept()
				if err != nil {
					return // listener closed
				}

				go func(c net.Conn, id string) {
					defer c.Close()
					buf := make([]byte, 1024)
					for {
						n, err := c.Read(buf)
						if err != nil {
							if err != io.EOF {
								t.Logf("Error reading from connection: %v", err)
							}
							return
						}

						// Echo the message back with server ID
						response := fmt.Sprintf("%s:%s", id, string(buf[:n]))
						_, err = c.Write([]byte(response))
						if err != nil {
							t.Logf("Error writing to connection: %v", err)
							return
						}
					}
				}(conn, id)
			}
		}(listener, serverID)
	}

	// Return address list and cleanup function
	cleanup := func() {
		for _, l := range listeners {
			l.Close()
		}
		wg.Wait()
	}

	return servers, cleanup
}

func TestNew(t *testing.T) {
	// Test creating a new proxy instance
	proxy := bproxy.New(8080, false)
	assert.NotNil(t, proxy)

	// We can't directly access fields now that we're using an interface
	// Instead, we'll test functionality through the interface methods

	// Test with debug enabled
	debugProxy := bproxy.New(9090, true)
	assert.NotNil(t, debugProxy)
}

func TestParseHexRoutes(t *testing.T) {
	tests := []struct {
		name       string
		mappings   []string
		wantErr    bool
		errMessage string
	}{
		{
			name:     "Valid mappings",
			mappings: []string{"0x01=server1:8081", "0x02=server2:8082"},
			wantErr:  false,
		},
		{
			name:       "Invalid format",
			mappings:   []string{"0x01=server1:8081", "invalid-format"},
			wantErr:    true,
			errMessage: "invalid mapping format: invalid-format (should be '0xXX=host:port')",
		},
		{
			name:       "Missing 0x prefix",
			mappings:   []string{"01=server1:8081"},
			wantErr:    true,
			errMessage: "byte should be in hex format with 0x prefix: 01",
		},
		{
			name:       "Invalid hex byte",
			mappings:   []string{"0xZZ=server1:8081"},
			wantErr:    true,
			errMessage: "failed to parse hex byte 0xZZ",
		},
		{
			name:       "Invalid destination format",
			mappings:   []string{"0x01=server1"},
			wantErr:    true,
			errMessage: "destination should be in host:port format: server1",
		},
		{
			name:       "Empty mappings",
			mappings:   []string{},
			wantErr:    true,
			errMessage: "no routes specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := bproxy.New(8080, false)
			err := proxy.ParseHexRoutes(tt.mappings)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMessage != "" {
					assert.Contains(t, err.Error(), tt.errMessage)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestProxyIntegration(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start test echo servers
	serverAddresses, cleanup := setupTestServers(t)
	defer cleanup()

	// Setup logger for testing
	logBuf, ctx := setupLogger(t)

	// Create and configure the proxy
	proxyPort := 49152 // use a high port to avoid conflicts
	proxy := bproxy.New(proxyPort, true)

	// Create mappings based on the actual server addresses
	mappings := []string{
		fmt.Sprintf("0x01=%s", serverAddresses[0]),
		fmt.Sprintf("0x02=%s", serverAddresses[1]),
	}

	err := proxy.ParseHexRoutes(mappings)
	require.NoError(t, err)

	// Start the proxy in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := proxy.Start(ctx)
		if err != nil {
			t.Logf("Proxy stopped with error: %v", err)
		}
	}()

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	// Test connections to both routes
	t.Run("Route to server1", func(t *testing.T) {
		testProxyConnection(t, proxyPort, 0x01, "Hello Server 1", "server1:Hello Server 1")
	})

	t.Run("Route to server2", func(t *testing.T) {
		testProxyConnection(t, proxyPort, 0x02, "Hello Server 2", "server2:Hello Server 2")
	})

	t.Run("Invalid route byte", func(t *testing.T) {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
		require.NoError(t, err)
		defer conn.Close()

		// Send an invalid route byte
		_, err = conn.Write([]byte{0x03})
		require.NoError(t, err)

		// Set a read deadline to avoid blocking
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		// The connection should be closed by the proxy due to invalid route
		buf := make([]byte, 1024)
		_, err = conn.Read(buf)
		assert.Error(t, err)
	})

	// Check logs
	logContent := logBuf.String()
	assert.Contains(t, logContent, "Byte proxy routing table")
	assert.Contains(t, logContent, "Route configured")
	assert.Contains(t, logContent, "Byte proxy listening")

	// Cleanup
	proxy.Shutdown(ctx)
	wg.Wait()
}

func testProxyConnection(t *testing.T, proxyPort int, routeByte byte, message, expectedResponse string) {
	// Connect to the proxy
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	require.NoError(t, err)
	defer conn.Close()

	// Send the routing byte followed by the message
	_, err = conn.Write([]byte{routeByte})
	require.NoError(t, err)

	// Send the test message
	_, err = conn.Write([]byte(message))
	require.NoError(t, err)

	// Read the response
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := conn.Read(buf)
	require.NoError(t, err)

	// Verify the response
	assert.Equal(t, expectedResponse, string(buf[:n]))
}

func TestSetupCleanupOnSignals(t *testing.T) {
	// This is just a basic test to ensure the method doesn't panic
	proxy := bproxy.New(8080, false)
	proxy.SetupCleanupOnSignals()

	// There's no easy way to test signal handling without sending actual signals,
	// which isn't ideal for unit tests. We just verify it doesn't panic.
}

func TestShutdown(t *testing.T) {
	// Create a proxy and start it
	_, ctx := setupLogger(t)
	proxy := bproxy.New(49153, false)

	// Configure the proxy with a simple route
	err := proxy.ParseHexRoutes([]string{"0x01=localhost:8081"})
	require.NoError(t, err)

	// Start the proxy in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = proxy.Start(ctx)
	}()

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown the proxy
	proxy.Shutdown(ctx)

	// Make sure the proxy has stopped (the listener is closed)
	wg.Wait()

	// Try to connect to the proxy - should fail
	_, err = net.Dial("tcp", "127.0.0.1:49153")
	assert.Error(t, err)
}
