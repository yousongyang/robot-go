package conn

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
