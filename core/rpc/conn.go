package rpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Conn reads and writes one JSON value per line.
type Conn struct {
	sc *bufio.Scanner
	mu sync.Mutex
	w  io.Writer
}

// NewConn wraps a reader and writer.
func NewConn(r io.Reader, w io.Writer) *Conn {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), MaxMessageBytes)
	return &Conn{sc: sc, w: w}
}

// ErrMessageTooLarge is returned for a line over MaxMessageBytes.
var ErrMessageTooLarge = errors.New("rpc: message too large")

// ReadLine returns the next non-empty line. It returns io.EOF at a clean end
// of stream and io.ErrUnexpectedEOF if the stream ends mid-line.
func (c *Conn) ReadLine() ([]byte, error) {
	for c.sc.Scan() {
		if b := c.sc.Bytes(); len(b) > 0 {
			return b, nil
		}
	}
	if err := c.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, ErrMessageTooLarge
		}
		return nil, err
	}
	return nil, io.EOF
}

// Write encodes v as one line. Safe for concurrent use.
func (c *Conn) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(b)
	return err
}
