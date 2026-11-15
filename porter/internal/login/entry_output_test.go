package login

import (
	"bytes"
	"io"
	"testing"
)

func TestTokenCommandOutputIsBoundedEvenThroughCopy(t *testing.T) {
	var output tokenOutput
	_, err := io.Copy(&output, bytes.NewReader(bytes.Repeat([]byte("fixture-output"), 100000)))
	if err == nil || len(output.Bytes()) > 256<<10 {
		t.Fatal("unbounded token command output", len(output.Bytes()), err)
	}
}
