package tide

import (
	"io"
	"strings"
)

// Overlay is the minimal surface a modal widget needs: where to draw, how big
// the screen is, and a callback to repaint the underlying screen before the
// modal draws itself on top. It lets widgets stay generic over the host app.
type Overlay struct {
	Out  io.Writer
	Size func() Size
	Base func() // repaint the screen under the modal (may be nil)
	Next func() (byte, error)
}

// Picker is a searchable, scrollable vertical list modal. Open blocks until
// the user selects an item (returns it with ok=true), cancels (ok=false), or
// the byte source errors.
type Picker struct {
	ov Overlay
}

// NewPicker returns a picker bound to an overlay surface.
func NewPicker(ov Overlay) *Picker { return &Picker{ov: ov} }

// Open runs the picker modal for the given title and items. Returns the
// selected item and true on confirm, "" and false on cancel/error/empty.
func (p *Picker) Open(title string, items []string) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	idx := 0
	query := ""
	filtered := append([]string(nil), items...)
	refilter := func() {
		filtered = filtered[:0]
		q := strings.ToLower(strings.TrimSpace(query))
		for _, item := range items {
			if q == "" || strings.Contains(strings.ToLower(item), q) {
				filtered = append(filtered, item)
			}
		}
		if idx >= len(filtered) {
			idx = len(filtered) - 1
		}
		if idx < 0 {
			idx = 0
		}
	}
	for {
		p.render(title, query, filtered, idx)
		b, err := p.ov.Next()
		if err != nil {
			p.repaint()
			return "", false
		}
		switch b {
		case 3:
			p.repaint()
			return "", false
		case 27:
			kind, _, _ := ReadEscape(p.ov.Next)
			switch kind {
			case EscUp:
				if idx > 0 {
					idx--
				}
			case EscDown:
				if idx+1 < len(filtered) {
					idx++
				}
			case EscPageUp:
				idx -= 10
				if idx < 0 {
					idx = 0
				}
			case EscPageDown:
				idx += 10
				if idx >= len(filtered) {
					idx = len(filtered) - 1
				}
			default:
				p.repaint()
				return "", false
			}
		case '\r', '\n':
			p.repaint()
			if len(filtered) == 0 {
				return "", false
			}
			return filtered[idx], true
		case 8, 127:
			if query != "" {
				query = query[:len(query)-1]
				refilter()
			}
		default:
			if b >= 32 {
				query += string(rune(b))
				refilter()
			}
		}
	}
}

func (p *Picker) repaint() {
	if p.ov.Base != nil {
		p.ov.Base()
	} else {
		WriteClipped(p.ov.Out, 1, 1, 80, "")
	}
}

func (p *Picker) render(title, query string, items []string, idx int) {
	if p.ov.Base != nil {
		p.ov.Base()
	}
	size := p.ov.Size()
	width := size.Cols - 8
	if width > 92 {
		width = 92
	}
	if width < 32 {
		width = 32
	}
	height := len(items) + 5
	maxHeight := size.Rows - 4
	if height > maxHeight {
		height = maxHeight
	}
	if height < 7 {
		height = 7
	}
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	DrawBox(p.ov.Out, top, left, width, height, title)
	WriteClipped(p.ov.Out, top+2, left, width, "| search: "+query)
	visible := height - 5
	start := 0
	if idx >= visible {
		start = idx - visible + 1
	}
	for row := 0; row < visible; row++ {
		itemIdx := start + row
		line := "| "
		if itemIdx < len(items) {
			marker := "  "
			if itemIdx == idx {
				marker = "> "
			}
			line += marker + items[itemIdx]
		}
		WriteClipped(p.ov.Out, top+4+row, left, width, line)
	}
}

// Prompt is a single-line text input modal. Open blocks until the user
// confirms (returns text, true), cancels (returns "", false), or the byte
// source errors.
type Prompt struct {
	ov Overlay
}

func NewPrompt(ov Overlay) *Prompt { return &Prompt{ov: ov} }

// Open runs the input modal. initial pre-fills the field.
func (p *Prompt) Open(title, promptLabel, initial string) (string, bool) {
	buf := []rune(initial)
	cursor := len(buf)
	for {
		p.render(title, promptLabel, string(buf), cursor)
		b, err := p.ov.Next()
		if err != nil {
			p.repaint()
			return "", false
		}
		switch b {
		case 3:
			p.repaint()
			return "", false
		case 27:
			kind, _, _ := ReadEscape(p.ov.Next)
			switch kind {
			case EscLeft:
				if cursor > 0 {
					cursor--
				}
			case EscRight:
				if cursor < len(buf) {
					cursor++
				}
			case EscHome:
				cursor = 0
			case EscEnd:
				cursor = len(buf)
			default:
				p.repaint()
				return "", false
			}
		case '\r', '\n':
			p.repaint()
			return string(buf), true
		case 8, 127:
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
			}
		default:
			if b >= 32 {
				r := rune(b)
				buf = append(buf[:cursor], append([]rune{r}, buf[cursor:]...)...)
				cursor++
			}
		}
	}
}

func (p *Prompt) repaint() {
	if p.ov.Base != nil {
		p.ov.Base()
	}
}

func (p *Prompt) render(title, promptLabel, text string, cursor int) {
	if p.ov.Base != nil {
		p.ov.Base()
	}
	size := p.ov.Size()
	width := size.Cols - 8
	if width > 80 {
		width = 80
	}
	if width < 32 {
		width = 32
	}
	height := 5
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	DrawBox(p.ov.Out, top, left, width, height, title)
	WriteClipped(p.ov.Out, top+2, left, width, "| "+promptLabel+text)
	WriteClipped(p.ov.Out, top+3, left, width, "| Enter save, Esc cancel")
	col := left + 2 + DisplayWidth(promptLabel+string([]rune(text)[:cursor]))
	MoveTo(p.ov.Out, top+2, col)
}
