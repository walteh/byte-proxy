# byte-proxy

A TCP proxy that routes connections based on the first byte sent by the client.

## Overview

byte-proxy is a specialized TCP proxy designed for protocols where the first byte of a connection can be used to determine routing. It reads the first byte of each incoming connection and forwards the connection to a specific destination based on a configured routing table. The first byte is stripped before forwarding the connection.

This is particularly useful for:

-   Multiplexing different services on the same port
-   Simple protocol-based routing without deep packet inspection
-   Testing and development environments where you need to route traffic based on a simple marker

## Features

-   Fast, byte-level TCP routing
-   Simple configuration via command-line flags
-   Hex-based routing rules (e.g., route 0x01 to one server, 0x02 to another)
-   Graceful shutdown with connection draining
-   Structured logging with optional debug output

## Installation

### Prerequisites

-   Go 1.21 or higher

### From Source

```bash
# Clone the repository
git clone https://github.com/walteh/byte-proxy.git
cd byte-proxy

# Build the binary
go build -o byte-proxy

# Run with -h to see available options
./byte-proxy -h
```

## Usage

```bash
# Start a proxy on port 9092 with two routing rules
byte-proxy --listen-port 9092 --map "0x01=server1:9094" --map "0x02=server2:9094"
```

### Command-line Options

| Flag            | Description                            | Default    |
| --------------- | -------------------------------------- | ---------- |
| `--listen-port` | Port to listen on                      | 9092       |
| `--map`         | Mapping in the format '0xXX=host:port' | (required) |
| `--debug`       | Enable debug logging                   | false      |

### Example Configuration

To route connections where:

-   First byte is 0x01 → service1:8080
-   First byte is 0x02 → service2:8080

```bash
byte-proxy --listen-port 9000 \
  --map "0x01=service1:8080" \
  --map "0x02=service2:8080"
```

## How It Works

1. The proxy listens for incoming TCP connections on the specified port
2. When a connection is established, it reads the first byte
3. It looks up the destination from the routing table based on the byte value
4. It establishes a connection to the target destination
5. It connects the incoming client to the target destination, bidirectionally proxying all further data
6. The first byte is NOT forwarded to the destination

## Development

The project uses a simple Go package structure:

-   `cmd/byte-proxy/`: Command-line interface
-   `pkg/bproxy/`: Core proxy implementation

### Building

```bash
# Build the project
go build

# Run tests
go test ./...
```

## License

See [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
