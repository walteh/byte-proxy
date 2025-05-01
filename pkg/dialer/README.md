# Byte-Proxy Dialer

The dialer package provides a custom implementation of Go's `net.Dialer` for communicating through a byte-proxy server. This dialer uses a routing byte as the first byte sent through a connection to determine where the connection should be proxied to.

## Features

-   Map multiple routing bytes to different proxy servers
-   Implements the `proxy.Dialer` interface from golang.org/x/net/proxy
-   Customizable timeout and keep-alive settings
-   Automatic fallback to direct connections for non-TCP protocols

## Usage

### Basic Usage

```go
import (
    "context"
    "github.com/walteh/byte-proxy/pkg/dialer"
)

// Create a map of routing bytes to proxy servers
routeMap := map[byte]string{
    1: "proxy1.example.com:8080",
    2: "proxy2.example.com:8080",
    3: "localhost:8080",
}

// Create a new proxy dialer with the route map
ctx := context.Background()
proxyDialer := dialer.New(ctx, routeMap)

// Connect using a specific route byte
conn, err := proxyDialer.DialWithByte(ctx, "tcp", "api.example.com:443", 2)
if err != nil {
    // Handle error
}
defer conn.Close()

// Use the connection as normal
conn.Write([]byte("Hello"))
```

### With HTTP Client

```go
httpClient := &http.Client{
    Transport: &http.Transport{
        DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            // Use route byte 3 for all HTTP connections
            return proxyDialer.DialWithByte(ctx, network, addr, 3)
        },
    },
}

// Make HTTP requests through the proxy
resp, err := httpClient.Get("http://example.com")
```

### Using Default Route

If you don't specify a route byte, the dialer will use the first route in the map:

```go
// Uses the first available route byte in the map
conn, err := proxyDialer.Dial("tcp", "example.com:80")
```

### Custom Timeouts

```go
// Set a custom timeout
proxyDialer = proxyDialer.WithTimeout(5 * time.Second)

// Set a custom keep-alive period
proxyDialer = proxyDialer.WithKeepAlive(60 * time.Second)
```

## How It Works

1. When you dial a connection, the dialer connects to the appropriate proxy server
2. It sends the routing byte as the first byte of data
3. The byte-proxy server routes the connection to the appropriate destination
4. All subsequent data is passed through transparently

The dialer automatically handles the routing byte, so you don't need to worry about it in your application code.
