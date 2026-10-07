package pagetree

import (
	"image"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// FromOCR builds a semantic page tree from an OCR annotation — the
// tree the Screen2x main-content model judges an image by.
//
// The old builder mapped OCR blocks to paragraph/staticText verbatim,
// so every tree degenerated to three roles (rootWebArea, paragraph,
// staticText) with each paragraph duplicating its single staticText
// child. The Screen2x GNN however has a dedicated categorical
// chrome_role input (screen2x_config.pbtxt: input_2_chrome_role) —
// Chrome's own trees always feed it heading/link/list/listItem roles.
// This builder reconstructs those from what the annotation carries:
//
//   - heading:  lines clearly larger than the body text (area-weighted
//     median height), with an estimated hierarchical_level from the
//     size ratio; wrapped headline lines merge into one heading.
//   - list:     bulleted/numbered lines (list/listItem), and horizontal
//     runs of short same-row lines — nav bars, footers, toolbars.
//   - link:     lines whose word ink is link-coloured (the engine
//     reports per-word foreground_rgb), or that carry a solid
//     underline when the pixels are available.
//   - separator: empty thin wide lines / content_type "separator".
//   - image:    content_type "image" lines.
//
// Paragraph lines merge across OCR blocks on real typography signals
// (vertical gap, left alignment, size, column overlap), so paragraphs
// contain several staticText children exactly like Chrome's trees
// instead of the paragraph=staticText duplication.
//
// img may be nil (OCR-JSON round-trips): link detection then relies
// on the engine's ink colours alone. opts selects which detectors
// run (settings group tree.*).
func FromOCR(ann *pb.Annotation, img *image.RGBA, imageW, imageH int, opts TreeOptions) *PageTree {
	opts = opts.normalize()
	root := NewNode("rootWebArea", "", [4]float64{0, 0, float64(imageW), float64(imageH)})
	infos, body := buildLineInfos(ann, img, opts)
	if len(infos) == 0 {
		return New(root, imageW, imageH)
	}

	// Page text column: used for "short" nav items and centering.
	colL, colR := textColumn(infos)

	// Horizontal runs of short same-row lines (nav bars, footers).
	navRuns := detectNavRuns(infos, colL, colR, opts)

	groups := segment(infos, navRuns, body, opts)
	if opts.DetectRegions {
		groups = wrapRegions(groups, infos, colL, colR, body)
	}
	for _, g := range groups {
		if node := g.node(colL, colR, body); node != nil {
			root.Children = append(root.Children, node)
		}
	}
	return New(root, imageW, imageH)
}

// lineInfo is one OCR line with geometry and derived classification.
type lineInfo struct {
	ln          *pb.Line
	x, y, w, h  float64
	text        string
	headingSize bool
	linkish     bool
	marker      bool // starts a list item ("•", "3.", ...)
	separator   bool
	imageLine   bool
}

// groupKind discriminates segment groups.
type groupKind int

const (
	gkParagraph groupKind = iota
	gkHeading
	gkList
	gkSeparator
	gkImage
	gkRegion
)

// lineGroup is one segment: a paragraph, heading, list (with items),
// separator, image line, or landmark region wrapping other groups.
type lineGroup struct {
	kind  groupKind
	lines []*lineInfo // paragraph/heading: all lines; list: in order
	items [][]*lineInfo
	// region fields (kind == gkRegion)
	region string       // "complementary" | "contentinfo"
	groups []*lineGroup // wrapped child groups
}

// buildLineInfos classifies every usable OCR line and returns the
// infos plus the body line height (area-weighted median, see
// bodyHeight).
func buildLineInfos(ann *pb.Annotation, img *image.RGBA, opts TreeOptions) ([]*lineInfo, float64) {
	var lines []*pb.Line
	for i := range ann.Lines {
		if ann.Lines[i].Box != nil {
			lines = append(lines, &ann.Lines[i])
		}
	}
	body := bodyHeight(lines)
	colL, colR := rawColumn(lines)
	analyzer := inkAnalyzer{img: img}

	var infos []*lineInfo
	for _, ln := range lines {
		b := ln.Box
		li := &lineInfo{
			ln: ln, x: float64(b.X), y: float64(b.Y),
			w: float64(b.Width), h: float64(b.Height),
			text: ln.Text, headingSize: opts.DetectHeadings && float64(b.Height) >= opts.HeadingMinRatio*body,
		}
		switch {
		case ln.ContentType == 2: // image
			li.imageLine = true
		case ln.ContentType == 4 || isThinRule(li, body, colL, colR):
			li.separator = true
		case ln.Text == "" || isWhitespace(ln.Text):
			continue // empty text and not a rule: model noise, drop
		default:
			li.marker = opts.DetectLists && isListItem(ln.Text)
			li.linkish = opts.DetectLinks && analyzer.lineLinkish(ln, li.headingSize)
		}
		infos = append(infos, li)
	}
	return infos, body
}

// isThinRule reports whether an (empty) line looks like a horizontal
// rule: very short and spanning a large share of the text column.
func isThinRule(li *lineInfo, body, colL, colR float64) bool {
	if li.text != "" {
		return false
	}
	colW := max(1.0, colR-colL)
	return li.h <= max(3.0, 0.2*body) && li.w >= 0.4*colW
}

func isWhitespace(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// textColumn returns the [left, right] extent of all recognized
// text over the classified infos.
func textColumn(infos []*lineInfo) (float64, float64) {
	l, r := 0.0, 0.0
	first := true
	for _, li := range infos {
		if li.separator || li.imageLine {
			continue
		}
		if first {
			l, r = li.x, li.x+li.w
			first = false
			continue
		}
		l = min(l, li.x)
		r = max(r, li.x+li.w)
	}
	if first || r <= l {
		return 0, max(1.0, float64(infos[0].w))
	}
	return l, r
}

// rawColumn is textColumn over raw pb lines (before classification).
func rawColumn(lines []*pb.Line) (float64, float64) {
	l, r := 0.0, 0.0
	first := true
	for _, ln := range lines {
		b := ln.Box
		if b == nil {
			continue
		}
		if first {
			l, r = float64(b.X), float64(b.X+b.Width)
			first = false
			continue
		}
		l = min(l, float64(b.X))
		r = max(r, float64(b.X+b.Width))
	}
	if first || r <= l {
		return 0, 1
	}
	return l, r
}

// sameRow reports whether two lines share a text row (strong vertical
// overlap).
func sameRow(a, b *lineInfo) bool {
	overlap := min(a.y+a.h, b.y+b.h) - max(a.y, b.y)
	return overlap >= 0.6*min(a.h, b.h)
}

// vGap is the vertical whitespace between two stacked lines.
func vGap(prev, cur *lineInfo) float64 { return cur.y - (prev.y + prev.h) }

// aligned reports whether two stacked lines start at the same column.
func aligned(a, b *lineInfo, tol float64) bool {
	return abs(a.x-b.x) <= tol
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// xOverlap returns the horizontal overlap of two lines.
func xOverlap(a, b *lineInfo) float64 {
	return min(a.x+a.w, b.x+b.w) - max(a.x, b.x)
}

// detectNavRuns finds maximal runs of consecutive short same-row
// lines (nav bars, footer link rows, toolbars). Returns a map from
// the index of the run's first line to the run length.
func detectNavRuns(infos []*lineInfo, colL, colR float64, opts TreeOptions) map[int]int {
	runs := map[int]int{}
	if !opts.DetectLists || len(infos) < 2 {
		return runs
	}
	colW := max(1.0, colR-colL)
	i := 0
	for i < len(infos) {
		if infos[i].separator || infos[i].imageLine || infos[i].marker {
			i++
			continue
		}
		j := i
		for j+1 < len(infos) && sameRow(infos[j], infos[j+1]) &&
			infos[j+1].w <= 0.45*colW && infos[j].w <= 0.45*colW &&
			max(infos[j].h, infos[j+1].h) <= 1.25*min(infos[j].h, infos[j+1].h) &&
			!infos[j+1].separator && !infos[j+1].imageLine && !infos[j+1].marker {
			j++
		}
		if j > i {
			runs[i] = j - i + 1
			i = j + 1
			continue
		}
		i++
	}
	return runs
}

// segment walks the classified lines in reading order and cuts them
// into paragraph / heading / list / separator / image groups.
func segment(infos []*lineInfo, navRuns map[int]int, body float64, opts TreeOptions) []*lineGroup {
	var groups []*lineGroup
	i := 0
	for i < len(infos) {
		li := infos[i]
		switch {
		case li.separator:
			groups = append(groups, &lineGroup{kind: gkSeparator, lines: []*lineInfo{li}})
			i++
		case li.imageLine:
			groups = append(groups, &lineGroup{kind: gkImage, lines: []*lineInfo{li}})
			i++
		case navRuns[i] > 0:
			n := navRuns[i]
			g := &lineGroup{kind: gkList}
			for k := 0; k < n; k++ {
				g.items = append(g.items, []*lineInfo{infos[i+k]})
				g.lines = append(g.lines, infos[i+k])
			}
			groups = append(groups, g)
			i += n
		case li.headingSize:
			g := &lineGroup{kind: gkHeading, lines: []*lineInfo{li}}
			h0 := li.h
			j := i + 1
			for j < len(infos) && infos[j].headingSize && !infos[j].separator && !infos[j].imageLine &&
				infos[j].h >= h0/1.15 && infos[j].h <= h0*1.15 &&
				(sameRow(infos[j-1], infos[j]) ||
					(vGap(infos[j-1], infos[j]) <= opts.ParagraphGap*body && aligned(infos[j-1], infos[j], 0.5*h0))) {
				g.lines = append(g.lines, infos[j])
				j++
			}
			groups = append(groups, g)
			i = j
		case li.marker:
			g, n := readList(infos, i, body, opts)
			groups = append(groups, g)
			i += n
		default:
			g, n := readParagraph(infos, i, body, opts, navRuns)
			groups = append(groups, g)
			i += n
		}
	}
	return groups
}

// readParagraph absorbs plain/linkish lines while typography says
// "same paragraph": small vertical gap, aligned left edge, similar
// size, shared column.
func readParagraph(infos []*lineInfo, start int, body float64, opts TreeOptions, navRuns map[int]int) (*lineGroup, int) {
	g := &lineGroup{kind: gkParagraph, lines: []*lineInfo{infos[start]}}
	last := infos[start]
	j := start + 1
	for j < len(infos) {
		cur := infos[j]
		if cur.separator || cur.imageLine || cur.marker || cur.headingSize || navRuns[j] > 0 {
			break
		}
		similar := max(last.h, cur.h) <= 1.33*min(last.h, cur.h)
		if sameRow(last, cur) {
			xgap := cur.x - (last.x + last.w)
			if !(xgap >= -0.5*cur.h && xgap <= 2.5*body && similar) {
				break
			}
		} else {
			gap := vGap(last, cur)
			if !(gap <= opts.ParagraphGap*body && aligned(last, cur, max(6.0, 0.55*body)) &&
				similar && xOverlap(last, cur) >= 0.3*min(last.w, cur.w)) {
				break
			}
		}
		g.lines = append(g.lines, cur)
		last = cur
		j++
	}
	return g, j - start
}

// readList absorbs a vertical bulleted/numbered list: marker lines
// start items, continuation lines extend the current item, the list
// ends at the first line that is neither.
func readList(infos []*lineInfo, start int, body float64, opts TreeOptions) (*lineGroup, int) {
	g := &lineGroup{kind: gkList}
	item := []*lineInfo{infos[start]}
	j := start + 1
	flush := func() {
		g.items = append(g.items, item)
		g.lines = append(g.lines, item...)
	}
	for j < len(infos) {
		cur := infos[j]
		switch {
		case cur.marker && vGap(infos[j-1], cur) <= 1.5*body:
			flush()
			item = []*lineInfo{cur}
			j++
		case !cur.marker && !cur.headingSize && !cur.separator && !cur.imageLine &&
			vGap(infos[j-1], cur) <= opts.ParagraphGap*body &&
			cur.x >= infos[start].x-0.5*body && cur.x <= infos[start].x+3*body:
			// wrapped continuation of the current item
			item = append(item, cur)
			j++
		default:
			flush()
			return g, j - start
		}
	}
	flush()
	return g, j - start
}

// node renders one segment group as accessibility Nodes.
func (g *lineGroup) node(colL, colR, body float64) *Node {
	switch g.kind {
	case gkSeparator:
		li := g.lines[0]
		return NewNode("separator", "", [4]float64{li.x, li.y, li.x + li.w, li.y + li.h})
	case gkImage:
		li := g.lines[0]
		return NewNode("image", "image", [4]float64{li.x, li.y, li.x + li.w, li.y + li.h})
	case gkHeading:
		return g.headingNode(colL, colR, body)
	case gkList:
		return g.listNode()
	case gkRegion:
		node := NewNode(g.region, "", g.box())
		for _, sub := range g.groups {
			if child := sub.node(colL, colR, body); child != nil {
				node.Children = append(node.Children, child)
			}
		}
		return node
	default:
		return g.paragraphNode(colL, colR)
	}
}

// lineNode emits one line as staticText, or as link > staticText when
// the line's ink says hyperlink.
func lineNode(li *lineInfo, weight float64) *Node {
	box := [4]float64{li.x, li.y, li.x + li.w, li.y + li.h}
	font := F64(fontPxFromHeight(li.h))
	if li.linkish {
		link := NewNode("link", "", box)
		link.Children = []*Node{{
			Role: "staticText", Text: li.text, Box: box,
			FontSize: font, FontWeight: F64(weight),
		}}
		return link
	}
	return &Node{Role: "staticText", Text: li.text, Box: box, FontSize: font, FontWeight: F64(weight)}
}

func (g *lineGroup) box() [4]float64 {
	first := true
	var box [4]float64
	for _, li := range g.lines {
		b := [4]float64{li.x, li.y, li.x + li.w, li.y + li.h}
		if first {
			box, first = b, false
			continue
		}
		box[0] = min(box[0], b[0])
		box[1] = min(box[1], b[1])
		box[2] = max(box[2], b[2])
		box[3] = max(box[3], b[3])
	}
	return box
}

// centered reports whether a group sits symmetrically inside the page
// text column with real margins on both sides — the /extras/styles/
// text-align "center" signal Chrome sends for centered content.
func (g *lineGroup) centered(colL, colR float64) bool {
	colW := colR - colL
	if colW <= 0 {
		return false
	}
	b := g.box()
	if b[2]-b[0] <= 0 {
		return false
	}
	leftM, rightM := b[0]-colL, colR-b[2]
	return abs(leftM-rightM) <= 0.1*colW && min(leftM, rightM) >= 0.05*colW
}

func (g *lineGroup) headingNode(colL, colR, body float64) *Node {
	if body <= 0 {
		body = g.medianH()
	}
	lvl := headingLevel(g.medianH() / body)
	node := &Node{
		Role: "heading", Box: g.box(), Level: &lvl,
		FontSize: F64(fontPxFromHeight(g.medianH())), FontWeight: F64(700.0),
	}
	if g.centered(colL, colR) {
		node.TextAlign = "center"
	}
	for _, li := range g.lines {
		node.Children = append(node.Children, lineNode(li, 700.0))
	}
	return node
}

func (g *lineGroup) paragraphNode(colL, colR float64) *Node {
	node := NewNode("paragraph", "", g.box())
	if g.centered(colL, colR) {
		node.TextAlign = "center"
	}
	for _, li := range g.lines {
		node.Children = append(node.Children, lineNode(li, 400.0))
	}
	return node
}

func (g *lineGroup) listNode() *Node {
	node := NewNode("list", "", g.box())
	for _, item := range g.items {
		itemNode := NewNode("listItem", "", unionBox(item))
		for _, li := range item {
			itemNode.Children = append(itemNode.Children, lineNode(li, 400.0))
		}
		node.Children = append(node.Children, itemNode)
	}
	return node
}

func (g *lineGroup) medianH() float64 {
	hs := make([]float64, 0, len(g.lines))
	for _, li := range g.lines {
		hs = append(hs, li.h)
	}
	// insertion sort: tiny slices
	for i := 1; i < len(hs); i++ {
		for j := i; j > 0 && hs[j] < hs[j-1]; j-- {
			hs[j], hs[j-1] = hs[j-1], hs[j]
		}
	}
	if len(hs) == 0 {
		return 16
	}
	return hs[len(hs)/2]
}

// wrapRegions groups side-column segments under role "complementary"
// (Chrome's aside landmark) and the trailing footer strip under role
// "contentinfo" (Chrome's footer landmark). Both are classic
// non-essential signals for Screen2x: on the news example the sidebar
// ad and the copyright line were wrongly kept as main content until
// they were wrapped in landmark containers.
//
// A side column is a run of groups that start beyond 55% of the text
// column width (or end before 45%), are narrow (<= half the column),
// and are vertically interleaved with lines that sit entirely on the
// other side — a lone short paragraph at the margin is NOT an aside.
// A footer is a trailing run after a gap of >= 2.5 body heights with
// body-or-smaller text.
func wrapRegions(groups []*lineGroup, infos []*lineInfo, colL, colR, body float64) []*lineGroup {
	if len(groups) == 0 {
		return groups
	}
	colW := colR - colL
	if colW <= 0 {
		return groups
	}

	classify := func(g *lineGroup) int { // 0 main, 1 right side, -1 left side
		switch g.kind {
		case gkSeparator:
			return 0 // separators do not break or join regions
		case gkImage:
			b := g.box()
			if b[2]-b[0] <= colW/2 && (b[0] >= colL+colW*0.55 || b[2] <= colL+colW*0.45) {
				return sideOf(b, colL, colR)
			}
			return 0
		}
		b := g.box()
		w := b[2] - b[0]
		if w <= 0 || w > colW/2 {
			return 0
		}
		if b[0] >= colL+colW*0.55 {
			return 1
		}
		if b[2] <= colL+colW*0.45 {
			return -1
		}
		return 0
	}

	// Side columns: maximal runs of same-side groups (separators
	// between same-side groups do not break the run).
	var out []*lineGroup
	i := 0
	for i < len(groups) {
		side := classify(groups[i])
		if side == 0 {
			out = append(out, groups[i])
			i++
			continue
		}
		j := i
		pending := 0
		for j < len(groups) {
			c := classify(groups[j])
			if c == side {
				pending = 0
				j++
			} else if c == 0 && groups[j].kind == gkSeparator && pending < 2 {
				pending++
				j++
			} else {
				break
			}
		}
		// A run qualifies as a side column only when it is interleaved
		// with lines from the other side; a lone short paragraph at
		// the margin (a wrapped body line, a small CTA) stays put.
		// A single heading never qualifies either.
		qualifies := j-i >= 2 || (j-i == 1 && groups[i].kind != gkHeading)
		if qualifies {
			for k := i; k < j; k++ {
				if groups[k].kind == gkSeparator {
					continue
				}
				if !interleavedWithOtherSide(groups[k], infos, side) {
					qualifies = false
					break
				}
			}
		}
		if qualifies {
			out = append(out, regionGroup("complementary", groups[i:j]))
		} else {
			out = append(out, groups[i:j]...)
		}
		i = j
	}
	groups = out

	// Footer: trailing groups after a large gap, body-or-smaller text.
	if len(groups) >= 2 {
		last := len(groups) - 1
		// walk back over separators
		end := last
		for end > 0 && groups[end].kind == gkSeparator {
			end--
		}
		if groups[end].kind == gkParagraph || groups[end].kind == gkList {
			gap := groups[end].box()[1] - prevBottom(groups, end)
			if gap >= 2.5*body {
				// extend the footer run over consecutive small trailing groups
				start := end
				for start > 0 {
					g := groups[start-1]
					if g.kind == gkSeparator {
						start--
						continue
					}
					if (g.kind == gkParagraph || g.kind == gkList) && g.box()[1] >= groups[end].box()[1]-2.5*body-body {
						start--
						continue
					}
					break
				}
				// separators at the run head stay top-level, not inside the footer
				for start < end && groups[start].kind == gkSeparator {
					start++
				}
				if start < end || (start == end && groups[start].kind == gkParagraph) {
					wrapped := append([]*lineGroup{}, groups[:start]...)
					wrapped = append(wrapped, regionGroup("contentinfo", groups[start:]))
					return wrapped
				}
			}
		}
	}
	return groups
}

// interleavedWithOtherSide reports whether the group vertically
// overlaps a line that lies entirely on the opposite side of the
// column — the geometric definition of "content beside this box".
func interleavedWithOtherSide(g *lineGroup, infos []*lineInfo, side int) bool {
	b := g.box() // [left, top, right, bottom]
	minH := b[3] - b[1]
	for _, li := range infos {
		// entirely on the other side?
		if side == 1 && li.x+li.w > b[0]+2 {
			continue // not entirely left of a right-side group
		}
		if side == -1 && li.x < b[2]-2 {
			continue // not entirely right of a left-side group
		}
		ov := min(b[3], li.y+li.h) - max(b[1], li.y)
		need := max(4.0, 0.15*min(minH, li.h))
		if ov >= need {
			return true
		}
	}
	return false
}

func sideOf(b [4]float64, colL, colR float64) int {
	if (b[0]+b[2])/2 >= (colL+colR)/2 {
		return 1
	}
	return -1
}

// prevBottom is the bottom edge of the last non-separator group before i.
func prevBottom(groups []*lineGroup, i int) float64 {
	bottom := 0.0
	for k := i - 1; k >= 0; k-- {
		if groups[k].kind == gkSeparator {
			continue
		}
		return groups[k].box()[3]
	}
	return bottom
}

// regionGroup wraps a run of groups under one landmark container
// (complementary / contentinfo).
func regionGroup(role string, groups []*lineGroup) *lineGroup {
	g := &lineGroup{kind: gkRegion, region: role}
	for _, sub := range groups {
		if sub.kind == gkSeparator {
			continue // keep separators out of landmark containers
		}
		g.lines = append(g.lines, sub.lines...)
		g.groups = append(g.groups, sub)
	}
	return g
}
func unionBox(lines []*lineInfo) [4]float64 {
	first := true
	var box [4]float64
	for _, li := range lines {
		b := [4]float64{li.x, li.y, li.x + li.w, li.y + li.h}
		if first {
			box, first = b, false
			continue
		}
		box[0] = min(box[0], b[0])
		box[1] = min(box[1], b[1])
		box[2] = max(box[2], b[2])
		box[3] = max(box[3], b[3])
	}
	return box
}
