package rpc

import (
	"bufio"
	"encoding/json"
	"io"
)

// Request is a minimal inbound RPC frame.
type Request struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Message  string `json:"message,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Event is a minimal outbound RPC frame.
type Event struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Delta   string `json:"delta,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Decoder reads newline-delimited JSON frames.
type Decoder struct {
	s *bufio.Scanner
}

// NewDecoder creates a frame decoder.
func NewDecoder(r io.Reader) *Decoder {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Decoder{s: s}
}

// Decode reads the next frame.
func (d *Decoder) Decode(v any) error {
	if !d.s.Scan() {
		if err := d.s.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(d.s.Bytes(), v)
}

// Encoder writes newline-delimited JSON frames.
type Encoder struct {
	w io.Writer
}

// NewEncoder creates a frame encoder.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode writes one frame.
func (e *Encoder) Encode(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = e.w.Write(append(data, '\n'))
	return err
}
