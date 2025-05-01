package dialer

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/proxy"
)

var _ proxy.Dialer = &ProxyDialer{}

// ProxyDialer is a custom net.Dialer that routes connections through different hosts
// based on a routing byte
type ProxyDialer struct {
	// RouteMap maps routing bytes to proxy host addresses
	routeMap map[string]byte

	// Timeout specifies the maximum amount of time a dial will wait for a connection
	Timeout time.Duration

	// KeepAlive specifies the keep-alive period for the connection
	KeepAlive time.Duration

	// FallbackDialer is the dialer to use if direct connections are needed
	FallbackDialer *net.Dialer

	Context context.Context

	ByteProxyAddress string
}

// DefaultTimeout is the default timeout used if none is specified
const DefaultTimeout = 30 * time.Second

// DefaultKeepAlive is the default keep-alive period used if none is specified
const DefaultKeepAlive = 30 * time.Second

// New creates a new ProxyDialer with the specified route map
func New(ctx context.Context, byteProxyAddress string, routes []string) *ProxyDialer {
	routeMap := make(map[string]byte)
	for i, route := range routes {
		routeMap[route] = byte(i)
	}
	return &ProxyDialer{
		Context:          ctx,
		ByteProxyAddress: byteProxyAddress,
		routeMap:         routeMap,
		Timeout:          DefaultTimeout,
		KeepAlive:        DefaultKeepAlive,
		FallbackDialer:   &net.Dialer{Timeout: DefaultTimeout, KeepAlive: DefaultKeepAlive},
	}
}

func (d *ProxyDialer) BProxyMappings() []string {
	mappings := []string{}
	for addr, route := range d.routeMap {
		mappings = append(mappings, fmt.Sprintf("0x%02x=%s", route, addr))
	}
	return mappings
}

// WithTimeout returns a new ProxyDialer with the specified timeout
func (d *ProxyDialer) WithTimeout(timeout time.Duration) *ProxyDialer {
	newDialer := *d
	newDialer.Timeout = timeout
	if newDialer.FallbackDialer != nil {
		fallback := *newDialer.FallbackDialer
		fallback.Timeout = timeout
		newDialer.FallbackDialer = &fallback
	}
	return &newDialer
}

// WithKeepAlive returns a new ProxyDialer with the specified keep-alive period
func (d *ProxyDialer) WithKeepAlive(keepAlive time.Duration) *ProxyDialer {
	newDialer := *d
	newDialer.KeepAlive = keepAlive
	if newDialer.FallbackDialer != nil {
		fallback := *newDialer.FallbackDialer
		fallback.KeepAlive = keepAlive
		newDialer.FallbackDialer = &fallback
	}
	return &newDialer
}

// Dial connects to the address using the byte-proxy
func (d *ProxyDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(d.Context, network, address)
}

// DialWithByte connects to the address using the byte-proxy with the specified routing byte
func (d *ProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		// Fall back to direct connection for non-TCP protocols
		if d.FallbackDialer != nil {
			return d.FallbackDialer.DialContext(ctx, network, address)
		}
		return nil, fmt.Errorf("unsupported network type: %s", network)
	}

	routeByte, ok := d.routeMap[address]
	if !ok {
		return nil, fmt.Errorf("no proxy address defined for routing byte: %d", routeByte)
	}

	// Use the fallback dialer settings to connect to the proxy
	dialer := net.Dialer{
		Timeout:   d.Timeout,
		KeepAlive: d.KeepAlive,
	}

	conn, err := dialer.DialContext(ctx, "tcp", d.ByteProxyAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy at %s: %w", d.ByteProxyAddress, err)
	}

	// Send the routing byte
	_, err = conn.Write([]byte{routeByte})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send routing byte to proxy: %w", err)
	}

	// Return the connection
	return &proxyConn{
		Conn:       conn,
		targetAddr: address,
		proxyAddr:  d.ByteProxyAddress,
		routeByte:  routeByte,
	}, nil
}

// proxyConn is a wrapper around net.Conn that tracks proxy information
type proxyConn struct {
	net.Conn
	targetAddr string
	proxyAddr  string
	routeByte  byte
}

// RemoteAddr returns the address of the target, not the proxy
func (c *proxyConn) RemoteAddr() net.Addr {
	// Return a custom address so that applications can see where they're "really" connected
	return &proxyAddr{
		network:    c.Conn.RemoteAddr().Network(),
		targetAddr: c.targetAddr,
		proxyAddr:  c.proxyAddr,
	}
}

// proxyAddr implements net.Addr for a proxied connection
type proxyAddr struct {
	network    string
	targetAddr string
	proxyAddr  string
}

// Network returns the network type
func (a *proxyAddr) Network() string {
	return a.network
}

// String returns a string representation of the address
func (a *proxyAddr) String() string {
	return fmt.Sprintf("%s (via proxy at %s)", a.targetAddr, a.proxyAddr)
}
