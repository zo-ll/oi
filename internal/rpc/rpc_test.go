package rpc

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestEncodeDecodeFrame(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(Request{ID: "1", Type: "prompt", Message: "hello"}); err != nil {
		t.Fatal(err)
	}

	dec := NewDecoder(&buf)
	var req Request
	if err := dec.Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.ID != "1" || req.Type != "prompt" || req.Message != "hello" {
		t.Fatalf("unexpected frame: %+v", req)
	}
}

func TestDecodeEOF(t *testing.T) {
	dec := NewDecoder(bytes.NewReader(nil))
	var req Request
	err := dec.Decode(&req)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
