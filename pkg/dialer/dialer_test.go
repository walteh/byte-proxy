package dialer

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mockByteProxyServer simulates a byte-proxy server for testing
// It accepts connections and responds based on the routing byte
type mockByteProxyServer struct {
	listener net.Listener
	routes   map[byte]mockRoute
	t        *testing.T
}

type mockRoute struct {
	response string
	delay    time.Duration
}

// startMockServer starts a mock byte-proxy server for testing
func startMockServer(t *testing.T, routes map[byte]mockRoute) (*mockByteProxyServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start mock server: %w", err)
	}

	server := &mockByteProxyServer{
		listener: l,
		routes:   routes,
		t:        t,
	}

	go server.serve()
	return server, nil
}

// serve handles incoming connections to the mock server
func (s *mockByteProxyServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Check if the server is closed
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			s.t.Logf("Error accepting connection: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

// handleConnection processes a single connection to the mock server
func (s *mockByteProxyServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read the routing byte
	routeByte := make([]byte, 1)
	_, err := conn.Read(routeByte)
	if err != nil {
		s.t.Logf("Error reading routing byte: %v", err)
		return
	}

	// Get the route
	route, exists := s.routes[routeByte[0]]
	if !exists {
		s.t.Logf("Unknown routing byte: %d", routeByte[0])
		return
	}

	// Simulate delay if configured
	if route.delay > 0 {
		time.Sleep(route.delay)
	}

	// Read any additional data (simulating the actual target address)
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil && err != io.EOF {
		s.t.Logf("Error reading data: %v", err)
		return
	}

	// Format response with the received data if any
	responseMsg := route.response
	if n > 0 {
		responseMsg = fmt.Sprintf("%s (received: %s)", route.response, buffer[:n])
	}

	// Send the response
	_, err = conn.Write([]byte(responseMsg))
	if err != nil {
		s.t.Logf("Error writing response: %v", err)
		return
	}
}

// close stops the mock server
func (s *mockByteProxyServer) close() {
	s.listener.Close()
}

// address returns the address of the mock server
func (s *mockByteProxyServer) address() string {
	return s.listener.Addr().String()
}

func TestDialer(t *testing.T) {
	// Create routes for the mock server
	routes := map[byte]mockRoute{
		0: {response: "Response from route 0", delay: 0},
		1: {response: "Response from route 1", delay: 10 * time.Millisecond},
		2: {response: "Response from route 2", delay: 0},
	}

	// Start the mock server
	server, err := startMockServer(t, routes)
	if err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer server.close()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Target addresses we'll use for testing
	targetAddresses := []string{
		"target1.example.com:80",
		"target2.example.com:443",
		"target3.example.com:8080",
	}

	// Create the dialer with the byte-proxy server address and target routes
	proxyDialer := New(ctx, server.address(), targetAddresses)
	proxyDialer = proxyDialer.WithTimeout(500 * time.Millisecond)

	// Test case 1: Dial with a valid address
	t.Run("DialWithValidAddress", func(t *testing.T) {
		conn, err := proxyDialer.Dial("tcp", targetAddresses[0])
		if err != nil {
			t.Fatalf("Failed to dial with address %s: %v", targetAddresses[0], err)
		}
		defer conn.Close()

		// Send a message
		message := "Hello from test"
		_, err = conn.Write([]byte(message))
		if err != nil {
			t.Fatalf("Failed to write message: %v", err)
		}

		// Read the response
		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}

		// Verify the response contains the expected text (from route 0)
		response := string(buffer[:n])
		if !strings.Contains(response, "Response from route 0") {
			t.Errorf("Unexpected response: %s", response)
		}

		// Verify the response contains the sent message
		if !strings.Contains(response, message) {
			t.Errorf("Response doesn't contain the sent message: %s", response)
		}

		// Check RemoteAddr
		addr := conn.RemoteAddr().String()
		if !strings.Contains(addr, targetAddresses[0]) {
			t.Errorf("Unexpected remote address: %s", addr)
		}
	})

	// Test case 2: Dial with a valid address that has a delay
	t.Run("DialWithDelayedRoute", func(t *testing.T) {
		conn, err := proxyDialer.Dial("tcp", targetAddresses[1])
		if err != nil {
			t.Fatalf("Failed to dial with address %s: %v", targetAddresses[1], err)
		}
		defer conn.Close()

		// Send a message
		_, err = conn.Write([]byte("Test with delay"))
		if err != nil {
			t.Fatalf("Failed to write message: %v", err)
		}

		// Read the response
		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			t.Fatalf("Failed to read response: %v", err)
		}

		// Verify the response contains the expected text (from route 1)
		response := string(buffer[:n])
		if !strings.Contains(response, "Response from route 1") {
			t.Errorf("Unexpected response: %s", response)
		}
	})

	// Test case 3: Dial with an invalid address (should fail to connect)
	t.Run("DialWithInvalidAddress", func(t *testing.T) {
		_, err := proxyDialer.Dial("tcp", "invalid.example.com:80")
		if err == nil {
			t.Fatal("Expected dial to fail with invalid address, but it succeeded")
		}
	})

	// Test case 4: Dial with non-TCP network (should use fallback dialer)
	t.Run("DialWithNonTCPNetwork", func(t *testing.T) {
		// Create a custom dialer to use as fallback for testing
		customDialer := New(ctx, server.address(), targetAddresses)
		// Override the internal FallbackDialer with a custom one that always returns an error
		customDialer.FallbackDialer = &net.Dialer{
			Control: func(_, _ string, _ syscall.RawConn) error {
				return fmt.Errorf("custom fallback dialer error")
			},
		}

		_, err := customDialer.Dial("udp", targetAddresses[0])
		if err == nil || !strings.Contains(err.Error(), "custom fallback dialer error") {
			t.Fatalf("Expected fallback dialer error, got: %v", err)
		}
	})

	// Test case 5: Context cancellation
	t.Run("ContextCancellation", func(t *testing.T) {
		// Create a context that's already canceled
		canceledCtx, cancelFunc := context.WithCancel(context.Background())
		cancelFunc()

		canceledDialer := New(canceledCtx, server.address(), targetAddresses)
		_, err := canceledDialer.Dial("tcp", targetAddresses[0])
		if err == nil {
			t.Fatal("Expected dial to fail with canceled context, but it succeeded")
		}
	})
}

func TestCustomTimeoutAndKeepAlive(t *testing.T) {
	ctx := context.Background()
	targetAddresses := []string{"example.com:8080"}

	// Create base dialer
	dialer := New(ctx, "proxy.example.com:8080", targetAddresses)

	// Test WithTimeout
	customTimeout := 5 * time.Second
	timeoutDialer := dialer.WithTimeout(customTimeout)
	if timeoutDialer.Timeout != customTimeout {
		t.Errorf("WithTimeout failed: expected %v, got %v", customTimeout, timeoutDialer.Timeout)
	}

	// Test WithKeepAlive
	customKeepAlive := 10 * time.Second
	keepAliveDialer := dialer.WithKeepAlive(customKeepAlive)
	if keepAliveDialer.KeepAlive != customKeepAlive {
		t.Errorf("WithKeepAlive failed: expected %v, got %v", customKeepAlive, keepAliveDialer.KeepAlive)
	}

	// Test that original dialer was not modified
	if dialer.Timeout != DefaultTimeout {
		t.Errorf("Original dialer was modified: Timeout %v should be %v", dialer.Timeout, DefaultTimeout)
	}
	if dialer.KeepAlive != DefaultKeepAlive {
		t.Errorf("Original dialer was modified: KeepAlive %v should be %v", dialer.KeepAlive, DefaultKeepAlive)
	}
}
