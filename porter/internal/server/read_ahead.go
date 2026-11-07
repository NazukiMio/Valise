package server

import (
	"context"
	"valise/core/rpc"
)

type incomingLine struct {
	line []byte
	err  error
}

// Reading independently lets EOF cancel the active handler while it waits on
// a configuration command. Requests themselves are still handled serially.
func readAhead(done <-chan struct{}, conn *rpc.Conn, cancel context.CancelFunc) <-chan incomingLine {
	lines := make(chan incomingLine)
	go func() {
		defer close(lines)
		for {
			line, err := conn.ReadLine()
			if err != nil {
				cancel()
			}
			select {
			case lines <- incomingLine{line: line, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return lines
}
