package tide

type Viewport struct {
	Lines  []string
	Height int
	Offset int
}

func (v *Viewport) SetLines(lines []string) {
	v.Lines = append(v.Lines[:0], lines...)
	v.Clamp()
}

func (v *Viewport) Scroll(delta int) {
	v.Offset += delta
	v.Clamp()
}

func (v *Viewport) Bottom() {
	v.Offset = len(v.Lines) - v.Height
	v.Clamp()
}

func (v *Viewport) Visible() []string {
	v.Clamp()
	if v.Height <= 0 || len(v.Lines) == 0 {
		return nil
	}
	end := v.Offset + v.Height
	if end > len(v.Lines) {
		end = len(v.Lines)
	}
	return v.Lines[v.Offset:end]
}

func (v *Viewport) Clamp() {
	if v.Height < 0 {
		v.Height = 0
	}
	max := len(v.Lines) - v.Height
	if max < 0 {
		max = 0
	}
	if v.Offset < 0 {
		v.Offset = 0
	}
	if v.Offset > max {
		v.Offset = max
	}
}
