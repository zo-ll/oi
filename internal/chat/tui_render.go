package chat

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zo-ll/tide"
)

func (a *tuiApp) renderSoon() {
	if time.Since(a.lastRender) < 33*time.Millisecond {
		return
	}
	a.render()
}

func (a *tuiApp) render() {
	a.lastRender = time.Now()
	size := a.term.Size()
	if size.Rows < 8 {
		size.Rows = 8
	}
	if size.Cols < 40 {
		size.Cols = 40
	}
	var frame strings.Builder
	tide.HideCursor(&frame)
	frame.WriteString("\x1b[H\x1b[J")
	header := fmt.Sprintf("oi  %s/%s  %s", valueOr(a.state.sel.Provider, "none"), valueOr(a.state.sel.Model, "none"), valueOr(a.state.rt.Policy.Root, "."))
	tide.WriteClipped(&frame, 1, 1, size.Cols, tide.Command(header))
	tide.WriteClipped(&frame, 2, 1, size.Cols, strings.Repeat("-", size.Cols))

	hints := a.commandHints()
	hintCount := 0
	if len(hints) > 0 {
		if a.hintIdx >= len(hints) {
			a.hintIdx = 0
		}
		hintCount = 5
		if hintCount > len(hints) {
			hintCount = len(hints)
		}
		maxHint := size.Rows - 6
		if maxHint < 1 {
			maxHint = 1
		}
		if hintCount > maxHint {
			hintCount = maxHint
		}
	}

	inputLines := a.renderInputLines(size.Cols)
	maxInputRows := size.Rows / 3
	if maxInputRows < 1 {
		maxInputRows = 1
	}
	if maxInputRows > 5 {
		maxInputRows = 5
	}
	inputStart := 0
	cursorLine := 0
	for i, line := range inputLines {
		if line.cursorCol >= 0 {
			cursorLine = i
			break
		}
	}
	if len(inputLines) > maxInputRows {
		if cursorLine >= maxInputRows {
			inputStart = cursorLine - maxInputRows + 1
		}
		if inputStart+maxInputRows > len(inputLines) {
			inputStart = len(inputLines) - maxInputRows
		}
		inputLines = inputLines[inputStart : inputStart+maxInputRows]
		cursorLine -= inputStart
	}

	bottomRows := 1 + len(inputLines) // separator + input lines
	if status := a.status; status != "" {
		bottomRows++
	}
	if hintCount > 0 {
		bottomRows += hintCount
	}
	viewHeight := size.Rows - 2 - bottomRows // 2 = header + separator
	if viewHeight < 1 {
		viewHeight = 1
	}

	lines := a.renderTranscript(size.Cols)
	maxScroll := len(lines) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	start := len(lines) - viewHeight - a.scroll
	if start < 0 {
		start = 0
	}
	end := start + viewHeight
	if end > len(lines) {
		end = len(lines)
	}
	for ri, line := range lines[start:end] {
		tide.WriteClipped(&frame, 3+ri, 1, size.Cols, line)
	}

	nextRow := 3 + viewHeight
	status := a.status
	if status != "" {
		tide.WriteClipped(&frame, nextRow, 1, size.Cols, tide.Dim(status))
		nextRow++
	}

	if hintCount > 0 {
		hintStart := 0
		if a.hintIdx >= hintCount {
			hintStart = a.hintIdx - hintCount + 1
		}
		if hintStart+hintCount > len(hints) {
			hintStart = len(hints) - hintCount
			if hintStart < 0 {
				hintStart = 0
			}
		}
		for r := 0; r < hintCount && hintStart+r < len(hints); r++ {
			i := hintStart + r
			marker := "  "
			if i == a.hintIdx {
				marker = "> "
			}
			tide.WriteClipped(&frame, nextRow+r, 1, size.Cols, tide.Dim(marker+hints[i]))
		}
		nextRow += hintCount
	}

	tide.WriteClipped(&frame, nextRow, 1, size.Cols, strings.Repeat("-", size.Cols))
	for i, line := range inputLines {
		tide.WriteClipped(&frame, nextRow+1+i, 1, size.Cols, line.text)
	}
	cursorRow := nextRow + 1 + cursorLine
	cursorCol := 1
	if cursorLine >= 0 && cursorLine < len(inputLines) {
		cursorCol += inputLines[cursorLine].cursorCol
	}
	if cursorCol > size.Cols {
		cursorCol = size.Cols
	}
	tide.MoveTo(&frame, cursorRow, cursorCol)
	tide.ShowCursor(&frame)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderInputLines(width int) []inputVisualLine {
	if width < 1 {
		width = 1
	}
	if len(a.input) == 0 {
		return []inputVisualLine{{text: "", start: 0, end: 0, cursorCol: 0}}
	}
	var lines []inputVisualLine
	start := 0
	used := 0
	for i, r := range a.input {
		rw := tide.DisplayWidth(string(r))
		if rw <= 0 {
			rw = 1
		}
		if used > 0 && used+rw > width {
			lines = append(lines, inputVisualLine{text: string(a.input[start:i]), start: start, end: i, cursorCol: -1})
			start = i
			used = 0
		}
		used += rw
	}
	lines = append(lines, inputVisualLine{text: string(a.input[start:]), start: start, end: len(a.input), cursorCol: -1})
	if a.cursor == len(a.input) && tide.DisplayWidth(lines[len(lines)-1].text) >= width {
		lines = append(lines, inputVisualLine{text: "", start: len(a.input), end: len(a.input), cursorCol: -1})
	}
	cursorLine := len(lines) - 1
	for i := range lines {
		line := &lines[i]
		if a.cursor >= line.start && a.cursor <= line.end {
			cursorLine = i
			if a.cursor == line.end && i+1 < len(lines) {
				cursorLine = i + 1
			}
			break
		}
	}
	if cursorLine < 0 {
		cursorLine = 0
	}
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}
	line := &lines[cursorLine]
	cursor := a.cursor
	if cursor < line.start {
		cursor = line.start
	}
	if cursor > line.end {
		cursor = line.end
	}
	line.cursorCol = tide.DisplayWidth(string(a.input[line.start:cursor]))
	return lines
}

func (a *tuiApp) renderTranscript(width int) []string {
	var lines []string
	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	for i := range a.blocks {
		block := &a.blocks[i]
		style := func(s string) string { return s }
		switch block.kind {
		case "user":
			style = tide.Command
		case "thinking":
			style = tide.Dim
		case "tool":
			style = tide.Dim
		case "assistant":
		case "error":
			style = func(s string) string { return tide.Warn("error: " + s) }
		default:
			style = tide.Dim
		}
		if block.wrapWidth != bodyWidth || block.wrapped == nil {
			block.wrapped = tide.WrapPlain(block.text, bodyWidth)
			block.wrapWidth = bodyWidth
			if len(block.wrapped) == 0 {
				block.wrapped = []string{""}
			}
		}
		for _, line := range block.wrapped {
			lines = append(lines, style(line))
		}
		lines = append(lines, "")
	}
	return lines
}
