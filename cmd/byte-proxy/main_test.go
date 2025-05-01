package byteproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRoutes(t *testing.T) {
	tests := []struct {
		name      string
		mappings  []string
		wantErr   bool
		errString string
		expected  []byteRoute
	}{
		{
			name:     "Valid mappings",
			mappings: []string{"0x01=localhost:8080", "0xFF=example.com:9000"},
			wantErr:  false,
			expected: []byteRoute{
				{byte: 0x01, destination: "localhost:8080", host: "localhost", port: "8080"},
				{byte: 0xFF, destination: "example.com:9000", host: "example.com", port: "9000"},
			},
		},
		{
			name:      "Missing 0x prefix",
			mappings:  []string{"01=localhost:8080"},
			wantErr:   true,
			errString: "byte must be in hex format with 0x prefix",
		},
		{
			name:      "Invalid hex value",
			mappings:  []string{"0xZZ=localhost:8080"},
			wantErr:   true,
			errString: "invalid hex byte",
		},
		{
			name:      "Too long hex value",
			mappings:  []string{"0x123=localhost:8080"},
			wantErr:   true,
			errString: "byte must be a single byte in hex format",
		},
		{
			name:      "Too short hex value",
			mappings:  []string{"0x1=localhost:8080"},
			wantErr:   true,
			errString: "byte must be a single byte in hex format",
		},
		{
			name:      "Invalid destination format",
			mappings:  []string{"0x01=localhost"},
			wantErr:   true,
			errString: "destination should be in host:port format",
		},
		{
			name:      "Empty mappings",
			mappings:  []string{},
			wantErr:   true,
			errString: "no routes specified",
		},
		{
			name:      "Invalid mapping format",
			mappings:  []string{"0x01:localhost:8080"},
			wantErr:   true,
			errString: "invalid mapping format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &ByteProxyCommand{
				Mappings: tt.mappings,
			}

			routes, err := cmd.parseRoutes()

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errString != "" {
					assert.Contains(t, err.Error(), tt.errString)
				}
				return
			}

			require.NoError(t, err)
			require.Equal(t, len(tt.expected), len(routes))

			for i, route := range routes {
				assert.Equal(t, tt.expected[i].byte, route.byte)
				assert.Equal(t, tt.expected[i].destination, route.destination)
				assert.Equal(t, tt.expected[i].host, route.host)
				assert.Equal(t, tt.expected[i].port, route.port)
			}
		})
	}
}

func TestHandleConnection(t *testing.T) {
	// Setup test routes
	routes := []byteRoute{
		{byte: 0x01, destination: "localhost:9001", host: "localhost", port: "9001"},
		{byte: 0x02, destination: "localhost:9002", host: "localhost", port: "9002"},
	}

	// Start mock destination servers
	destWg := sync.WaitGroup{}
	destWg.Add(2)

	// Start server 1 (for byte 0x01)
	server1, err := startMockServer(t, "localhost:9001", []byte("response from server 1"), &destWg)
	require.NoError(t, err)
	defer server1.Close()

	// Start server 2 (for byte 0x02)
	server2, err := startMockServer(t, "localhost:9002", []byte("response from server 2"), &destWg)
	require.NoError(t, err)
	defer server2.Close()

	ctx := context.Background()
	cmd := &ByteProxyCommand{}

	t.Run("Valid route 0x01", func(t *testing.T) {
		// Create client and server pipe
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		// Handle connection in a goroutine
		var wg sync.WaitGroup
		wg.Add(1)

		var handlerErr error
		go func() {
			defer wg.Done()
			handlerErr = cmd.handleConnection(ctx, serverConn, routes)
		}()

		// Send the routing byte
		_, err := clientConn.Write([]byte{0x01})
		require.NoError(t, err)

		// Then send some data
		_, err = clientConn.Write([]byte("test message"))
		require.NoError(t, err)

		// Read the response
		response := make([]byte, 512)
		clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := clientConn.Read(response)
		require.NoError(t, err)

		assert.Equal(t, "response from server 1", string(response[:n]))

		// Close connections and wait for handler to complete
		clientConn.Close()
		wg.Wait()

		// Check for handler errors
		assert.NoError(t, handlerErr)
	})

	t.Run("Valid route 0x02", func(t *testing.T) {
		// Create client and server pipe
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		// Handle connection in a goroutine
		var wg sync.WaitGroup
		wg.Add(1)

		var handlerErr error
		go func() {
			defer wg.Done()
			handlerErr = cmd.handleConnection(ctx, serverConn, routes)
		}()

		// Send the routing byte
		_, err := clientConn.Write([]byte{0x02})
		require.NoError(t, err)

		// Then send some data
		_, err = clientConn.Write([]byte("test message"))
		require.NoError(t, err)

		// Read the response
		response := make([]byte, 512)
		clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := clientConn.Read(response)
		require.NoError(t, err)

		assert.Equal(t, "response from server 2", string(response[:n]))

		// Close connections and wait for handler to complete
		clientConn.Close()
		wg.Wait()

		// Check for handler errors
		assert.NoError(t, handlerErr)
	})

	t.Run("Invalid route byte", func(t *testing.T) {
		// Create client and server pipe
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		// Handle connection in a goroutine
		var wg sync.WaitGroup
		wg.Add(1)

		var handlerErr error
		go func() {
			defer wg.Done()
			handlerErr = cmd.handleConnection(ctx, serverConn, routes)
		}()

		// Send an invalid routing byte
		_, err := clientConn.Write([]byte{0x03})
		require.NoError(t, err)

		// Close connections and wait for handler to complete
		clientConn.Close()
		wg.Wait()

		// We should get an error about no route found
		assert.Error(t, handlerErr)
		assert.Contains(t, handlerErr.Error(), "no route found for byte")
	})

	// Wait for the destination servers to complete
	destWg.Wait()
}

func TestIntegration(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start destination servers
	destWg := sync.WaitGroup{}
	destWg.Add(2)

	// Start server 1 (for byte 0x01)
	server1, err := startMockServer(t, "localhost:9091", []byte("response from server 1"), &destWg)
	require.NoError(t, err)
	defer server1.Close()

	// Start server 2 (for byte 0x02)
	server2, err := startMockServer(t, "localhost:9092", []byte("response from server 2"), &destWg)
	require.NoError(t, err)
	defer server2.Close()

	// Create and run the proxy in the background
	cmd := &ByteProxyCommand{
		ListenPort: 10099,
		Mappings:   []string{"0x01=localhost:9091", "0x02=localhost:9092"},
		Debug:      false,
	}

	// Create a context with cancel for the proxy
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the proxy in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.run(nil, nil)
	}()

	// Wait for the proxy to start
	time.Sleep(100 * time.Millisecond)

	// Test connection to server 1
	t.Run("Connect to server 1", func(t *testing.T) {
		conn, err := net.Dial("tcp", "localhost:10099")
		require.NoError(t, err)
		defer conn.Close()

		// Send the routing byte (0x01) and a test message
		_, err = conn.Write([]byte{0x01, 'h', 'e', 'l', 'l', 'o'})
		require.NoError(t, err)

		// Read the response
		response := make([]byte, 512)
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := conn.Read(response)
		require.NoError(t, err)

		assert.Equal(t, "response from server 1", string(response[:n]))
	})

	// Test connection to server 2
	t.Run("Connect to server 2", func(t *testing.T) {
		conn, err := net.Dial("tcp", "localhost:10099")
		require.NoError(t, err)
		defer conn.Close()

		// Send the routing byte (0x02) and a test message
		_, err = conn.Write([]byte{0x02, 'h', 'e', 'l', 'l', 'o'})
		require.NoError(t, err)

		// Read the response
		response := make([]byte, 512)
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := conn.Read(response)
		require.NoError(t, err)

		assert.Equal(t, "response from server 2", string(response[:n]))
	})

	// Test invalid route
	t.Run("Invalid route", func(t *testing.T) {
		conn, err := net.Dial("tcp", "localhost:10099")
		require.NoError(t, err)
		defer conn.Close()

		// Send an invalid routing byte (0x03)
		_, err = conn.Write([]byte{0x03, 'h', 'e', 'l', 'l', 'o'})
		require.NoError(t, err)

		// The connection should close
		response := make([]byte, 512)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, err = conn.Read(response)
		assert.Error(t, err) // Either timeout or EOF
	})

	// Shutdown the proxy
	cancel()

	// Wait for the proxy to shut down
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Proxy did not shut down in time")
	}

	// Wait for the destination servers to complete
	destWg.Wait()
}

// Helper to start a mock TCP server that returns a fixed response
func startMockServer(t *testing.T, addr string, response []byte, wg *sync.WaitGroup) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Listener closed
				return
			}

			go func(c net.Conn) {
				defer c.Close()

				// Read client message (don't care about the content)
				buf := make([]byte, 1024)
				_, err := c.Read(buf)
				if err != nil && err != io.EOF {
					t.Logf("Error reading from client: %v", err)
					return
				}

				// Write response
				_, err = c.Write(response)
				if err != nil {
					t.Logf("Error writing to client: %v", err)
					return
				}
			}(conn)
		}
	}()

	return listener, nil
}

// Benchmark the proxy with various payload sizes
func BenchmarkProxy(b *testing.B) {
	// Setup test routes
	routes := []byteRoute{
		{byte: 0x01, destination: "localhost:9001", host: "localhost", port: "9001"},
	}

	// Start mock destination server
	destWg := sync.WaitGroup{}
	destWg.Add(1)

	echoServer, err := startEchoServer(b, "localhost:9001", &destWg)
	require.NoError(b, err)
	defer echoServer.Close()

	ctx := context.Background()
	cmd := &ByteProxyCommand{}

	// Test different payload sizes
	payloadSizes := []int{10, 100, 1000, 10000, 100000}

	for _, size := range payloadSizes {
		payload := bytes.Repeat([]byte("a"), size)

		b.Run(fmt.Sprintf("Payload-%dB", size), func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Create client and server pipe
				clientConn, serverConn := net.Pipe()

				// Handle connection in a goroutine
				var wg sync.WaitGroup
				wg.Add(1)

				go func() {
					defer wg.Done()
					cmd.handleConnection(ctx, serverConn, routes)
				}()

				// Send the routing byte
				clientConn.Write([]byte{0x01})
				b.StartTimer()

				// Send payload
				clientConn.Write(payload)

				// Read response
				response := make([]byte, size)
				clientConn.Read(response)

				b.StopTimer()
				// Clean up
				clientConn.Close()
				wg.Wait()
			}
		})
	}

	// Wait for the echo server to complete
	destWg.Wait()
}

// Helper to start an echo server that echoes back whatever it receives
func startEchoServer(b *testing.B, addr string, wg *sync.WaitGroup) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Listener closed
				return
			}

			go func(c net.Conn) {
				defer c.Close()

				// Echo all data back
				io.Copy(c, c)
			}(conn)
		}
	}()

	return listener, nil
}
