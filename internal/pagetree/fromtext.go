package pagetree

// FromText stacks paragraphs of plain text in a simple reading column.
func FromText(text string, pageW int) *PageTree {
	root := NewNode("rootWebArea", "", [4]float64{0, 0, float64(pageW), 0})
	y := 24.0
	for _, para := range splitParagraphs(text) {
		lines := wrapPlain(para, float64(pageW)-48, 16.0)
		pnode := NewNode("paragraph", "", [4]float64{24, y, float64(pageW) - 24, y})
		for _, ln := range lines {
			pnode.Children = append(pnode.Children, &Node{
				Role:     "staticText",
				Text:     ln,
				Box:      [4]float64{24, y, float64(pageW) - 24, y + 23},
				FontSize: F64(16.0),
			})
			y += 23.0
		}
		pnode.Box = [4]float64{24, pnode.Box[1], float64(pageW) - 24, y}
		y += 14.0
		root.Children = append(root.Children, pnode)
	}
	root.Box = [4]float64{0, 0, float64(pageW), max(y+24, 100)}
	return New(root, pageW, int(root.Box[3]))
}

func splitParagraphs(text string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(text) {
		if text[i] == '\n' && i+1 < len(text) && text[i+1] == '\n' {
			if s := trimSpace(text[start:i]); s != "" {
				out = append(out, s)
			}
			i += 2
			start = i
			continue
		}
		i++
	}
	if s := trimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

func wrapPlain(text string, width, font float64) []string {
	maxChars := max(8, int(width/(font*0.52)))
	words := fields(text)
	var lines []string
	cur := ""
	for _, word := range words {
		trial := word
		if cur != "" {
			trial = cur + " " + word
		}
		if len([]rune(trial)) <= maxChars || cur == "" {
			cur = trial
		} else {
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func fields(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && isSpaceByte(s[i]) {
			i++
		}
		start := i
		for i < len(s) && !isSpaceByte(s[i]) {
			i++
		}
		if i > start {
			out = append(out, s[start:i])
		}
	}
	return out
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpaceByte(s[start]) {
		start++
	}
	for end > start && isSpaceByte(s[end-1]) {
		end--
	}
	return s[start:end]
}
