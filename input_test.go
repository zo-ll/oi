package tide

import (
	"testing"
)

func TestReadEscapeArrowAndMouse(t *testing.T) {
	cases := []struct {
		name string
		seq  []byte
		want EscapeKind
	}{
		{"up", []byte{'[', 'A'}, EscUp},
		{"down", []byte{'[', 'B'}, EscDown},
		{"right", []byte{'[', 'C'}, EscRight},
		{"left", []byte{'[', 'D'}, EscLeft},
		{"home", []byte{'[', 'H'}, EscHome},
		{"end", []byte{'[', '4', '~'}, EscEnd},
		{"page-up", []byte{'[', '5', '~'}, EscPageUp},
		{"page-down", []byte{'[', '6', '~'}, EscPageDown},
		{"scroll-up", []byte{'[', '<', '6', '4', ';', '3', '3', ';', '3', '1', 'M'}, EscScrollUp},
		{"scroll-down", []byte{'[', '<', '6', '5', ';', '3', '3', ';', '3', '1', 'M'}, EscScrollDn},
		{"mouse-click", []byte{'[', '<', '0', ';', '3', '3', ';', '3', '1', 'M'}, EscMouse},
		{"bare-esc-cancel", []byte{'x'}, EscCancel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := 0
			next := func() (byte, error) {
				if i >= len(c.seq) {
					t.Fatalf("ran out of bytes for %s", c.name)
				}
				b := c.seq[i]
				i++
				return b, nil
			}
			kind, _, err := ReadEscape(next)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if kind != c.want {
				t.Fatalf("kind = %q, want %q", kind, c.want)
			}
		})
	}
}
