package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walteh/byte-proxy/pkg/dialer"
)

// setupEchoServer creates a simple TCP echo server for testing
func setupEchoServer(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Get the assigned port
	address := listener.Addr().String()

	// Start the echo server in a goroutine
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Server is shutting down
				return
			}

			// Handle each connection in a goroutine
			go func(c net.Conn) {
				defer c.Close()

				// Echo received data back with server ID
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						if err != io.EOF {
							t.Logf("Error reading from connection: %v", err)
						}
						return
					}

					// Echo the message back with "ECHO:" prefix
					_, err = c.Write([]byte("ECHO:" + string(buf[:n])))
					if err != nil {
						t.Logf("Error writing to connection: %v", err)
						return
					}
				}
			}(conn)
		}
	}()

	// Return the server address and a cleanup function
	cleanup := func() {
		listener.Close()
	}

	return address, cleanup
}

// TestIntegration runs the byte-proxy binary and tests basic functionality
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start an echo server to use as a target
	serverAddr, cleanup := setupEchoServer(t)
	defer cleanup()

	// Define the "target" we want to route to through the proxy
	targetAddress := "echo.service:80"

	// Find an available port for the proxy
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyPort := strings.Split(proxyListener.Addr().String(), ":")[1]
	proxyListener.Close() // Close it so the proxy can use it

	// Build the byte-proxy binary
	buildCmd := exec.Command("go", "build", "-o", "byte-proxy-test")
	buildOutput, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binary: %s", buildOutput)
	defer os.Remove("byte-proxy-test") // Clean up the binary

	// Prepare command to run the proxy with the echo server as target
	// This maps our virtual "echo.service:80" to the actual echo server address
	cmd := exec.Command("./byte-proxy-test",
		"--listen-port", proxyPort,
		"--map", "0x00="+serverAddr, // Maps byte 0 to our echo server
		"--debug")

	// Capture command output
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Start the proxy
	err = cmd.Start()
	require.NoError(t, err, "Failed to start proxy")

	// Ensure we clean up at the end
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait() // Wait for the process to finish
		}
	}()

	// Wait for proxy to start
	time.Sleep(1 * time.Second)

	// Create a context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build proxy address
	proxyAddr := "127.0.0.1:" + proxyPort

	// Test Part 1: Traditional direct connection (for reference)
	t.Run("DirectConnection", func(t *testing.T) {
		// Connect directly to the proxy (without the dialer package)
		directConn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err, "Failed to connect to proxy directly")
		defer directConn.Close()

		// Send the routing byte (0x00) followed by test data
		testData := []byte("Hello via direct connection!")
		_, err = directConn.Write([]byte{0x00})
		require.NoError(t, err, "Failed to send routing byte")

		// Wait a bit to ensure the routing byte is processed
		time.Sleep(100 * time.Millisecond)

		_, err = directConn.Write(testData)
		require.NoError(t, err, "Failed to send test data")

		// Set a timeout for reading the response
		err = directConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		require.NoError(t, err)

		// Read and verify the response
		response := make([]byte, 1024)
		n, err := directConn.Read(response)
		require.NoError(t, err, "Failed to read response from direct connection")

		// Verify the echo server prefixed our data with "ECHO:"
		expected := "ECHO:" + string(testData)
		assert.Equal(t, expected, string(response[:n]), "Incorrect response data from direct connection")
	})

	// Test Part 2: Using the dialer package
	t.Run("DialerPackage", func(t *testing.T) {
		// Create our dialer with the target route mapping
		proxyDialer := dialer.New(ctx, proxyAddr, []string{targetAddress})

		// Connect via the dialer package which handles routing byte
		conn, err := proxyDialer.Dial("tcp", targetAddress)
		require.NoError(t, err, "Failed to connect using the dialer package")
		defer conn.Close()

		// Send test data - the dialer has already sent the routing byte
		testData := []byte("Hello via dialer package!")
		_, err = conn.Write(testData)
		require.NoError(t, err, "Failed to send test data through dialer")

		// Set a timeout for reading the response
		err = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		require.NoError(t, err)

		// Read and verify the response
		response := make([]byte, 1024)
		n, err := conn.Read(response)
		require.NoError(t, err, "Failed to read response through dialer")

		// Verify the echo server prefixed our data with "ECHO:"
		expected := "ECHO:" + string(testData)
		assert.Equal(t, expected, string(response[:n]), "Incorrect response data through dialer")

		// Check RemoteAddr to verify the connection wrapper works properly
		addr := conn.RemoteAddr().String()
		assert.Contains(t, addr, targetAddress, "RemoteAddr doesn't contain the target address")
		assert.Contains(t, addr, "proxy", "RemoteAddr doesn't indicate proxy connection")
	})

	// Log proxy output
	t.Logf("Proxy stdout: %s", stdout.String())
	t.Logf("Proxy stderr: %s", stderr.String())
}
