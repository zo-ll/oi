package tide

import (
	"io"
	"strings"
)

// WriteClipped moves to (row,col), clears the line, and writes text clipped to
// width display cells. It is the shared primitive for frame-based rendering
// where every cell is painted at an absolute position.
func WriteClipped(w io.Writer, row, col, width int, text string) {
	MoveTo(w, row, col)
	ClearLine(w)
	runes := []rune(text)
	for len(runes) > 0 && DisplayWidth(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	_, _ = io.WriteString(w, string(runes))
}

// DrawBox draws a bordered modal frame with a title on the top border.
// top/left are the corner coordinates, width/height the outer dimensions.
// The interior is not cleared row-by-row; callers fill content rows with
// WriteClipped. height must be >= 4 to fit top border, title/subtitle, a
// separator, at least one content row, and a bottom border.
func DrawBox(w io.Writer, top, left, width, height int, title string) {
	if width < 6 {
		width = 6
	}
	if height < 4 {
		height = 4
	}
	border := "+" + strings.Repeat("-", width-2) + "+"
	WriteClipped(w, top, left, width, border)
	if title != "" {
		WriteClipped(w, top+1, left, width, "| "+title)
	} else {
		WriteClipped(w, top+1, left, width, "|")
	}
	WriteClipped(w, top+2, left, width, "+"+strings.Repeat("-", width-2)+"+")
	for r := top + 3; r < top+height-1; r++ {
		WriteClipped(w, r, left, width, "|")
	}
	WriteClipped(w, top+height-1, left, width, border)
}

// WrapPlain wraps text by paragraph (newline-separated), with each paragraph
// wrapped to width display cells. Empty paragraphs produce a single blank
// line. It is the paragraph-aware counterpart to Wrap, which wraps a single
// line.
func WrapPlain(text string, width int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		wrapped := Wrap(para, width)
		if len(wrapped) == 0 {
			out = append(out, "")
			continue
		}
		out = append(out, wrapped...)
	}
	return out
}

// HeaderLine writes a two-column "left  right" help/status line padded to
// width and clipped. Used by command help and status rows.
func HeaderLine(w io.Writer, left, right string, width int, row, col int) {
	gap := 2
	text := left
	room := width - DisplayWidth(left) - DisplayWidth(right) - gap
	if room > 0 {
		text = left + strings.Repeat(" ", room) + "  " + right
	}
	WriteClipped(w, row, col, width, text)
}
