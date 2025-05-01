package bproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

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
	Routes     []Route
	Debug      bool

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// New creates a new MapProxy instance
func New(listenPort int, debug bool) *MapProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &MapProxy{
		ListenPort: listenPort,
		Debug:      debug,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// ParseHexRoutes parses byte routing rules from string mappings
// Each mapping must be in the format "0xXX=host:port"
func (p *MapProxy) ParseHexRoutes(mappings []string) error {
	var routes []Route

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

		routes = append(routes, Route{
			Byte:        b,
			Destination: dest,
			Host:        hostPort[0],
			Port:        hostPort[1],
		})
	}

	if len(routes) == 0 {
		return fmt.Errorf("no routes specified")
	}

	p.Routes = routes
	return nil
}

// Start begins listening and routing connections
func (p *MapProxy) Start(ctx context.Context) error {
	logger := zerolog.Ctx(ctx)
	if logger == nil {
		logger = &zerolog.Logger{}
	}

	// Print routing table
	logger.Info().Msg("Byte proxy routing table:")
	for _, route := range p.Routes {
		logger.Info().
			Int("byte", int(route.Byte)).
			Str("destination", route.Destination).
			Msg("Route configured")
	}

	// Create listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", p.ListenPort))
	if err != nil {
		return fmt.Errorf("failed to start listener on port %d: %w", p.ListenPort, err)
	}
	p.listener = listener

	logger.Info().Int("port", p.ListenPort).Msg("Byte proxy listening")

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

				// Handle the connection
				err := p.handleConnection(ctx, clientConn)
				if err != nil {
					logger.Error().Err(err).Msg("Connection handling error")
				}
			}(conn)
		}
	}()

	// Wait for context cancel or accept error
	select {
	case err := <-acceptError:
		logger.Error().Err(err).Msg("Error accepting connections")
		p.Shutdown()
		return err
	case <-ctx.Done():
		p.Shutdown()
		return nil
	}
}

// handleConnection processes an incoming connection
func (p *MapProxy) handleConnection(ctx context.Context, clientConn net.Conn) error {
	logger := zerolog.Ctx(ctx)

	// Read the first byte to determine the target
	firstByte := make([]byte, 1)
	_, err := clientConn.Read(firstByte)
	if err != nil {
		return fmt.Errorf("failed to read routing byte: %w", err)
	}

	// Find the matching route
	var targetRoute *Route
	for i, route := range p.Routes {
		if route.Byte == firstByte[0] {
			targetRoute = &p.Routes[i]
			break
		}
	}

	if targetRoute == nil {
		return fmt.Errorf("no route found for byte: %d", firstByte[0])
	}

	logger.Debug().
		Int("byte", int(firstByte[0])).
		Str("destination", targetRoute.Destination).
		Msg("Routing connection")

	// Connect to the target
	targetConn, err := net.Dial("tcp", targetRoute.Destination)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", targetRoute.Destination, err)
	}
	defer targetConn.Close()

	// Copy data in both directions
	errCh := make(chan error, 2)

	// Client -> Target (don't forward the first byte, it was already consumed)
	go func() {
		_, err := io.Copy(targetConn, clientConn)
		errCh <- err
	}()

	// Target -> Client
	go func() {
		_, err := io.Copy(clientConn, targetConn)
		errCh <- err
	}()

	// Wait for either direction to finish or error
	var copyErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && err != io.EOF {
			copyErr = err
		}
	}

	return copyErr
}

// Shutdown stops the proxy gracefully
func (p *MapProxy) Shutdown() {
	if p.listener != nil {
		p.listener.Close()
	}

	// Wait for active connections to finish with timeout
	waitCh := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		// All connections closed gracefully
	case <-time.After(5 * time.Second):
		// Timeout waiting for connections to close
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
		p.Shutdown()
		os.Exit(0)
	}()
}
