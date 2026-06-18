package chat

import (
	"io"
	"strings"

	"github.com/zo-ll/tide"
)

func (a *tuiApp) overlayPicker(title string, items []string) (string, bool) {
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
		a.renderPickerOverlay(title, query, filtered, idx)
		b, err := a.nextByte()
		if err != nil {
			a.render()
			return "", false
		}
		switch b {
		case 3:
			a.render()
			return "", false
		case 27:
			kind := a.readOverlayEscape()
			switch kind {
			case "up":
				if idx > 0 {
					idx--
				}
			case "down":
				if idx+1 < len(filtered) {
					idx++
				}
			case "page-up":
				idx -= 10
				if idx < 0 {
					idx = 0
				}
			case "page-down":
				idx += 10
				if idx >= len(filtered) {
					idx = len(filtered) - 1
				}
			default:
				a.render()
				return "", false
			}
		case '\r', '\n':
			a.render()
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

func (a *tuiApp) overlayInput(title, prompt, initial string) (string, bool) {
	buf := []rune(initial)
	cursor := len(buf)
	for {
		a.renderInputOverlay(title, prompt, string(buf), cursor)
		b, err := a.nextByte()
		if err != nil {
			a.render()
			return "", false
		}
		switch b {
		case 3:
			a.render()
			return "", false
		case 27:
			kind := a.readOverlayEscape()
			switch kind {
			case "left":
				if cursor > 0 {
					cursor--
				}
			case "right":
				if cursor < len(buf) {
					cursor++
				}
			case "home":
				cursor = 0
			case "end":
				cursor = len(buf)
			default:
				a.render()
				return "", false
			}
		case '\r', '\n':
			a.render()
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

func (a *tuiApp) renderApprovalOverlay(action, target string) {
	a.render()
	size := a.term.Size()
	width := size.Cols - 8
	if width > 92 {
		width = 92
	}
	if width < 40 {
		width = 40
	}
	bodyWidth := width - 4
	targetLines := wrapPlain(target, bodyWidth)
	if len(targetLines) > 8 {
		targetLines = append(targetLines[:8], "...")
	}
	height := len(targetLines) + 6
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
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+tide.Warn("approve "+action)+strings.Repeat(" ", width))
	writeClipped(&frame, top+2, left, width, "+"+strings.Repeat("-", width-2)+"+")
	row := top + 3
	for _, line := range targetLines {
		writeClipped(&frame, row, left, width, "| "+line)
		row++
	}
	for row < top+height-2 {
		writeClipped(&frame, row, left, width, "|")
		row++
	}
	writeClipped(&frame, top+height-2, left, width, "| "+tide.Command("y")+" approve   "+tide.Command("n")+" deny   Esc cancel")
	writeClipped(&frame, top+height-1, left, width, border)
	tide.MoveTo(&frame, top+height-2, left+2)
	tide.ShowCursor(&frame)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderPickerOverlay(title, query string, items []string, idx int) {
	a.render()
	size := a.term.Size()
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
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+title)
	writeClipped(&frame, top+2, left, width, "| search: "+query)
	writeClipped(&frame, top+3, left, width, "+"+strings.Repeat("-", width-2)+"+")
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
		writeClipped(&frame, top+4+row, left, width, line)
	}
	writeClipped(&frame, top+height-1, left, width, border)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderInputOverlay(title, prompt, text string, cursor int) {
	a.render()
	size := a.term.Size()
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
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+title)
	writeClipped(&frame, top+2, left, width, "| "+prompt+text)
	writeClipped(&frame, top+3, left, width, "| Enter save, Esc cancel")
	writeClipped(&frame, top+4, left, width, border)
	col := left + 2 + tide.DisplayWidth(prompt+string([]rune(text)[:cursor]))
	tide.MoveTo(&frame, top+2, col)
	_, _ = io.WriteString(a.term.Out, frame.String())
}
