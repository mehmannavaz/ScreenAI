package pagetree

import (
	"regexp"
	"strings"
)

// mdHeadingRe matches ATX markdown headings ("# ...", "## ...").
var mdHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// FromText builds a semantic tree from plain text or markdown: "#"
// lines become headings with their level (when detect_headings is
// on), list-marker lines become list/listItem nodes (detect_lists),
// "> " lines become blockquotes, and blank lines split paragraphs —
// so even the text path feeds Screen2x the same role variety Chrome
// does.
func FromText(text string, pageW int, opts TreeOptions) *PageTree {
	opts = opts.normalize()
	root := NewNode("rootWebArea", "", [4]float64{0, 0, float64(pageW), 0})
	x, w := 24.0, float64(pageW)-48.0
	y := 24.0

	addHeading := func(text string, level int) {
		font := headingFont(level)
		pad := 10.0 + 6.0*float64(6-level)
		node := &Node{
			Role: "heading", Level: &level,
			Box:        [4]float64{x, y, x + w, y + font*1.3 + pad},
			FontSize:   F64(font),
			FontWeight: F64(700.0),
			TextAlign:  "left",
		}
		for _, ln := range wrapPlain(text, w-8, font) {
			node.Children = append(node.Children, &Node{
				Role: "staticText", Text: ln,
				Box:        [4]float64{x, y, x + w, y + font*1.3},
				FontSize:   F64(font),
				FontWeight: F64(700.0),
			})
			y += font * 1.3
		}
		node.Box[3] = y + pad/2
		y += pad
		root.Children = append(root.Children, node)
	}

	addParagraph := func(text string, indent float64) {
		node := NewNode("paragraph", "", [4]float64{x + indent, y, x + w - indent, y})
		for _, ln := range wrapPlain(text, w-indent, 16.0) {
			node.Children = append(node.Children, &Node{
				Role: "staticText", Text: ln,
				Box:      [4]float64{x + indent, y, x + w - indent, y + 23},
				FontSize: F64(16.0),
			})
			y += 23.0
		}
		node.Box = [4]float64{x + indent, node.Box[1], x + w - indent, y}
		y += 14.0
		root.Children = append(root.Children, node)
	}

	addBlockquote := func(text string) {
		node := NewNode("blockquote", "", [4]float64{x + 20, y, x + w, y})
		for _, ln := range wrapPlain(text, w-36, 16.0) {
			node.Children = append(node.Children, &Node{
				Role: "staticText", Text: ln,
				Box:        [4]float64{x + 32, y, x + w - 4, y + 23},
				FontSize:   F64(16.0),
				FontWeight: F64(400.0),
			})
			y += 23.0
		}
		node.Box = [4]float64{x + 20, node.Box[1], x + w, y}
		y += 14.0
		root.Children = append(root.Children, node)
	}

	addList := func(items []string) {
		node := NewNode("list", "", [4]float64{x, y, x + w, y})
		for _, item := range items {
			itemNode := NewNode("listItem", "", [4]float64{x + 12, y, x + w, y})
			lines := wrapPlain(item, w-24, 16.0)
			for _, ln := range lines {
				itemNode.Children = append(itemNode.Children, &Node{
					Role: "staticText", Text: ln,
					Box:      [4]float64{x + 16, y, x + w, y + 23},
					FontSize: F64(16.0),
				})
				y += 23.0
			}
			itemNode.Box = [4]float64{x + 12, itemNode.Box[1], x + w, y}
			node.Children = append(node.Children, itemNode)
			y += 4.0
		}
		node.Box = [4]float64{x, node.Box[1], x + w, y}
		y += 12.0
		root.Children = append(root.Children, node)
	}

	// Parse: markdown headings, list markers, blockquotes, setext
	// underlines, blank-line-separated paragraphs.
	type block struct {
		kind  string // heading | para | quote | list
		text  string
		level int
		items []string
	}
	var blocks []block
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(raw); i++ {
		line := strings.TrimSpace(raw[i])
		if line == "" {
			continue
		}
		if opts.DetectHeadings {
			if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
				blocks = append(blocks, block{kind: "heading", text: strings.TrimSpace(m[2]), level: len(m[1])})
				continue
			}
			// setext heading: the NEXT line is "===" (h1) or "---" (h2)
			if i+1 < len(raw) && isSetextUnderline(strings.TrimSpace(raw[i+1])) {
				level := 2
				if strings.Trim(raw[i+1], "=") == "" {
					level = 1
				}
				blocks = append(blocks, block{kind: "heading", text: line, level: level})
				i++ // underline consumed
				continue
			}
		}
		if opts.DetectLists && isListMarkerLine(line) {
			// collect consecutive list items (continuation lines join)
			var items []string
			cur := stripListMarker(line)
			for i+1 < len(raw) {
				next := strings.TrimSpace(raw[i+1])
				if next == "" {
					break
				}
				if isListMarkerLine(next) {
					items = append(items, cur)
					cur = stripListMarker(next)
					i++
					continue
				}
				// indented continuation of the current item
				if isIndented(raw[i+1]) {
					cur += " " + next
					i++
					continue
				}
				break
			}
			items = append(items, cur)
			blocks = append(blocks, block{kind: "list", items: items})
			continue
		}
		if strings.HasPrefix(line, ">") {
			quote := strings.TrimSpace(strings.TrimPrefix(line, ">"))
			for i+1 < len(raw) && strings.HasPrefix(strings.TrimSpace(raw[i+1]), ">") {
				quote += " " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw[i+1]), ">"))
				i++
			}
			blocks = append(blocks, block{kind: "quote", text: quote})
			continue
		}
		// plain paragraph: absorb continuation lines
		para := line
		for i+1 < len(raw) {
			next := strings.TrimSpace(raw[i+1])
			if next == "" || isListMarkerLine(next) || strings.HasPrefix(next, ">") ||
				mdHeadingRe.MatchString(next) || isSetextUnderline(next) {
				break
			}
			para += " " + next
			i++
		}
		blocks = append(blocks, block{kind: "para", text: para})
	}

	for _, b := range blocks {
		switch b.kind {
		case "heading":
			addHeading(b.text, b.level)
		case "quote":
			addBlockquote(b.text)
		case "list":
			addList(b.items)
		default:
			addParagraph(b.text, 0)
		}
	}

	root.Box = [4]float64{0, 0, float64(pageW), max(y+24, 100)}
	return New(root, pageW, int(root.Box[3]))
}

// headingFont maps a heading level to its estimated pixel size,
// following the classic h1..h6 type scale over a 16px body.
func headingFont(level int) float64 {
	switch level {
	case 1:
		return 32
	case 2:
		return 24
	case 3:
		return 20
	case 4:
		return 17
	case 5:
		return 15
	default:
		return 13
	}
}

// isSetextUnderline reports whether a line is a setext heading
// underline ("===" or "---", possibly longer).
func isSetextUnderline(line string) bool {
	if len(line) < 2 {
		return false
	}
	trimmedEq := strings.Trim(line, "=")
	trimmedDash := strings.Trim(line, "- ")
	return trimmedEq == "" || (trimmedDash == "" && strings.Contains(line, "-"))
}

func isListMarkerLine(line string) bool { return listMarkerRe.MatchString(line) }

func stripListMarker(line string) string {
	if loc := listMarkerRe.FindStringIndex(line); loc != nil {
		return strings.TrimSpace(line[loc[1]:])
	}
	return strings.TrimSpace(line)
}

// isIndented reports whether a raw (untrimmed) line starts with two
// or more spaces of indentation and still has content — a wrapped
// list item continuation.
func isIndented(raw string) bool {
	n := 0
	for _, r := range raw {
		if r == ' ' || r == '\t' {
			n++
			continue
		}
		break
	}
	return n >= 2 && strings.TrimSpace(raw) != ""
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
