package chat

import (
	"github.com/zo-ll/tide"
)

// overlaySurface builds the tide.Overlay the modal widgets draw on: the
// terminal writer, its size, the base repaint callback, and the async byte
// source (which also pumps model events and approval responses).
func (a *tuiApp) overlaySurface() tide.Overlay {
	return tide.Overlay{
		Out:  a.term.Out,
		Size: a.term.Size,
		Base: a.render,
		Next: a.nextByte,
	}
}

func (a *tuiApp) overlayPicker(title string, items []string) (string, bool) {
	return tide.NewPicker(a.overlaySurface()).Open(title, items)
}

func (a *tuiApp) overlayInput(title, prompt, initial string) (string, bool) {
	return tide.NewPrompt(a.overlaySurface()).Open(title, prompt, initial)
}

// renderApprovalOverlay draws the y/n approval modal. It is oi-specific (the
// action/target domain and channel round-trip belong to the agent layer) but
// reuses tide.DrawBox / tide.WriteClipped for the frame.
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
	targetLines := tide.WrapPlain(target, bodyWidth)
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
	tide.DrawBox(a.term.Out, top, left, width, height, tide.Warn("approve "+action))
	row := top + 3
	for _, line := range targetLines {
		tide.WriteClipped(a.term.Out, row, left, width, "| "+line)
		row++
	}
	tide.WriteClipped(a.term.Out, top+height-2, left, width, "| "+tide.Command("y")+" approve   "+tide.Command("n")+" deny   Esc cancel")
	tide.MoveTo(a.term.Out, top+height-2, left+2)
	tide.ShowCursor(a.term.Out)
}
