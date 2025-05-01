package bproxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Proxy interface defines the methods that any proxy implementation must support
type Proxy interface {
	ParseHexRoutes(mappings []string) error
	Start(ctx context.Context) error
	Shutdown(ctx context.Context)
	SetupCleanupOnSignals()
}

// Route represents a routing rule based on the first byte of a connection
type Route struct {
	Byte        byte
	Destination string
	Host        string
	Port        string
}

// MapProxy represents a proxy that routes connections based on the first byte
type MapProxy struct {
	ListenPort int
	Routes     map[byte]Route
	Debug      bool

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// New creates a new MapProxy instance
func New(listenPort int, debug bool) Proxy {
	// We don't create a context here anymore, it will come from the caller
	return &MapProxy{
		ListenPort: listenPort,
		Debug:      debug,
	}
}

// ParseHexRoutes parses byte routing rules from string mappings
// Each mapping must be in the format "0xXX=host:port"
func (p *MapProxy) ParseHexRoutes(mappings []string) error {
	routes := make(map[byte]Route)

	for _, mapping := range mappings {
		parts := strings.Split(mapping, "=")
		if len(parts) != 2 {
			return fmt.Errorf("invalid mapping format: %s (should be '0xXX=host:port')", mapping)
		}

		// Parse the byte in hex format (e.g., 0x01)
		byteStr := parts[0]
		if !strings.HasPrefix(byteStr, "0x") {
			return fmt.Errorf("byte should be in hex format with 0x prefix: %s", byteStr)
		}

		var b byte
		_, err := fmt.Sscanf(byteStr, "0x%x", &b)
		if err != nil {
			return fmt.Errorf("failed to parse hex byte %s: %w", byteStr, err)
		}

		// Parse the destination
		dest := parts[1]
		hostPort := strings.Split(dest, ":")
		if len(hostPort) != 2 {
			return fmt.Errorf("destination should be in host:port format: %s", dest)
		}

		routes[b] = Route{
			Byte:        b,
			Destination: dest,
			Host:        hostPort[0],
			Port:        hostPort[1],
		}
	}

	if len(routes) == 0 {
		return fmt.Errorf("no routes specified")
	}

	p.Routes = routes
	return nil
}

// Start begins listening and routing connections
func (p *MapProxy) Start(ctx context.Context) error {
	// Store context for later use and setup cancel function
	var cancel context.CancelFunc
	p.ctx, cancel = context.WithCancel(ctx)
	p.cancel = cancel

	// Print routing table
	slog.InfoContext(ctx, "Byte proxy routing table:")
	for _, route := range p.Routes {
		slog.InfoContext(ctx, "Route configured",
			"byte", int(route.Byte),
			"destination", route.Destination)
	}

	// Create listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", p.ListenPort))
	if err != nil {
		return fmt.Errorf("failed to start listener on port %d: %w", p.ListenPort, err)
	}
	p.listener = listener

	slog.InfoContext(ctx, "Byte proxy listening", "port", p.ListenPort)

	// Accept connections in a goroutine
	acceptError := make(chan error, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Check if we're shutting down
				select {
				case <-ctx.Done():
					return
				default:
					acceptError <- err
					return
				}
			}

			// Handle each connection in a goroutine
			p.wg.Add(1)
			go func(clientConn net.Conn) {
				defer p.wg.Done()
				defer clientConn.Close()

				// Handle the connection - pass the current context
				connCtx, cancel := context.WithCancel(ctx)
				defer cancel()

				err := p.handleConnection(connCtx, clientConn)
				if err != nil {
					slog.ErrorContext(ctx, "Connection handling error", "error", err)
				}
			}(conn)
		}
	}()

	// Wait for context cancel or accept error
	select {
	case err := <-acceptError:
		slog.ErrorContext(ctx, "Error accepting connections", "error", err)
		p.Shutdown(ctx)
		return err
	case <-ctx.Done():
		p.Shutdown(ctx)
		return ctx.Err()
	}
}

// handleConnection processes an incoming connection
func (p *MapProxy) handleConnection(ctx context.Context, clientConn net.Conn) error {
	// Get remote address for logging
	remoteAddr := clientConn.RemoteAddr().String()

	// Read the first byte to determine the target
	firstByte := make([]byte, 1)
	_, err := clientConn.Read(firstByte)
	if err != nil {
		return fmt.Errorf("failed to read routing byte from %s: %w", remoteAddr, err)
	}

	// Find the matching route
	targetRoute, ok := p.Routes[firstByte[0]]
	if !ok {
		return fmt.Errorf("no route found for byte %d from %s", firstByte[0], remoteAddr)
	}

	if p.Debug {
		slog.DebugContext(ctx, "Routing connection",
			"byte", int(firstByte[0]),
			"destination", targetRoute.Destination,
			"client", remoteAddr)
	}

	// Create a connection-specific timeout context
	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()

	// Connect to the target using timeout context
	var dialer net.Dialer
	targetConn, err := dialer.DialContext(dialCtx, "tcp", targetRoute.Destination)
	if err != nil {
		return fmt.Errorf("failed to connect to %s for client %s: %w",
			targetRoute.Destination, remoteAddr, err)
	}
	defer targetConn.Close()

	// Copy data in both directions with context cancellation
	errCh := make(chan error, 2)

	// Set up context cancellation for copy operations
	copyCtx, copyCancel := context.WithCancel(ctx)
	defer copyCancel()

	// Monitor context for cancellation
	go func() {
		<-copyCtx.Done()
		// Force connections to close on context cancellation
		clientConn.Close()
		targetConn.Close()
	}()

	// Client -> Target (don't forward the first byte, it was already consumed)
	go func() {
		written, err := io.Copy(targetConn, clientConn)
		if p.Debug && err == nil {
			slog.DebugContext(ctx, "Connection closed",
				"direction", "client->target",
				"bytes", written,
				"client", remoteAddr)
		}
		errCh <- err
	}()

	// Target -> Client
	go func() {
		written, err := io.Copy(clientConn, targetConn)
		if p.Debug && err == nil {
			slog.DebugContext(ctx, "Connection closed",
				"direction", "target->client",
				"bytes", written,
				"client", remoteAddr)
		}
		errCh <- err
	}()

	// Wait for either direction to finish or error or context cancellation
	var copyErr error
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil && err != io.EOF {
				copyErr = err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return copyErr
}

// Shutdown stops the proxy gracefully
func (p *MapProxy) Shutdown(ctx context.Context) {
	slog.InfoContext(ctx, "Shutting down proxy")

	// Close the listener to stop accepting new connections
	if p.listener != nil {
		p.listener.Close()
	}

	// Create a timeout context for waiting for connections to close
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Wait for active connections to finish with timeout
	waitCh := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		slog.InfoContext(ctx, "All connections closed gracefully")
	case <-shutdownCtx.Done():
		slog.InfoContext(ctx, "Timeout waiting for connections to close")
	}

	// Call cancel function to release resources
	if p.cancel != nil {
		p.cancel()
	}
}

// SetupCleanupOnSignals registers signal handlers for graceful shutdown
func (p *MapProxy) SetupCleanupOnSignals() {
	// Create a channel to receive signals
	sigChan := make(chan os.Signal, 1)

	// Register for SIGTERM and SIGINT
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Start a goroutine to handle signals
	go func() {
		<-sigChan
		// We need a context for shutdown
		ctx := context.Background()
		slog.InfoContext(ctx, "Received signal, shutting down")
		p.Shutdown(ctx)
		os.Exit(0)
	}()
}
