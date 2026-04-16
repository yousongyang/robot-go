package conn

import "flag"

// Connection abstracts a network connection for reading/writing binary messages.
type Connection interface {
	// ReadMessage reads a binary message from the connection.
	ReadMessage() ([]byte, error)

	// WriteMessage writes binary data to the connection.
	WriteMessage(data []byte) error

	// Close gracefully closes the connection.
	Close() error

	// IsValid returns true if the connection is established and usable.
	IsValid() bool

	// IsUnexpectedCloseError returns true if the error represents an unexpected connection close.
	IsUnexpectedCloseError(err error) bool
}

// NewConnectFunc creates a Connection
type NewConnectFunc func() (Connection, error)

func RegisterFlags(flagSet *flag.FlagSet) *flag.FlagSet {
	flagSet.String("url", "ws://localhost:7001/ws/v1", "server url")
	flagSet.String("connect-type", "websocket", "websocket, atgateway ...")
	flagSet.String("access-token", "", "atgateway Mod: access token (enables gateway protocol)")
	flagSet.String("key-exchange", "none", "atgateway Mod: ECDH key exchange: none, x25519, p256/p-256, p384/p-384, p521/p-521")
	flagSet.String("crypto", "none", "atgateway Mod: crypto algorithm list: none, xxtea, aes-128-cbc, aes-192-cbc, aes-256-cbc, aes-128-gcm, aes-192-gcm, aes-256-gcm, chacha20, chacha20-poly1305, xchacha20-poly1305")
	flagSet.String("compression", "none", "atgateway Mod: compression algorithm list: none, zstd, lz4, snappy, zlib")
	return flagSet
}
