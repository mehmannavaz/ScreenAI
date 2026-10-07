package pagetree

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// roleFor maps HTML tags to Chrome accessibility roles — the same table
// Chromium's AX name generation uses for the common tags.
var roleFor = map[string]string{
	"html": "rootWebArea", "body": "genericContainer",
	"main": "main", "article": "article", "aside": "complementary",
	"nav": "navigation", "header": "banner", "footer": "contentinfo",
	"h1": "heading", "h2": "heading", "h3": "heading", "h4": "heading",
	"h5": "heading", "h6": "heading",
	"p": "paragraph", "a": "link", "button": "button", "img": "image",
	"ul": "list", "ol": "list", "li": "listItem", "hr": "separator",
	"input": "textField", "select": "popUpButton", "textarea": "textField",
	"table": "table", "thead": "tableHeaderContainer",
	"tbody": "genericContainer",
	"tr":    "row", "td": "cell", "th": "columnheader",
	"blockquote": "blockquote", "pre": "pre", "code": "code",
	"figure": "figure", "figcaption": "caption",
	"section": "section", "form": "form", "label": "label",
	"strong": "strong", "b": "strong", "em": "emphasis", "i": "emphasis",
	"small": "small", "mark": "mark", "time": "time",
	"svg": "image", "canvas": "image", "video": "video",
	"hgroup": "genericContainer", "dl": "list", "dt": "term", "dd": "definition",
}

// inlineSemantic lists inline elements that become their own
// accessibility nodes (like Chrome's AX trees: paragraph > link >
// staticText) instead of being folded into the surrounding text.
// Generic inline wrappers (span, u, sub, ...) are folded.
var inlineSemantic = map[string]bool{
	"a": true, "strong": true, "b": true, "em": true, "i": true,
	"code": true, "mark": true, "small": true, "time": true,
}

// fontFor estimates the font size (px) per tag.
var fontFor = map[string]float64{
	"h1": 32.0, "h2": 24.0, "h3": 20.0, "h4": 17.0, "h5": 15.0,
	"h6": 13.0, "p": 16.0, "li": 16.0, "a": 16.0, "small": 13.0,
	"nav": 14.0, "header": 14.0, "footer": 12.0, "aside": 13.0,
	"button": 14.0, "input": 14.0, "label": 14.0, "td": 15.0,
	"th": 15.0, "blockquote": 16.0, "pre": 14.0, "code": 14.0,
	"dt": 16.0, "dd": 16.0, "figcaption": 13.0, "caption": 13.0,
}

var boldTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "strong": true, "th": true, "button": true, "b": true,
}

var indentFor = map[string]float64{
	"blockquote": 32, "dd": 32, "li": 24, "pre": 12, "td": 8, "th": 8,
}

// domEl is one element of a tolerant DOM: children are either *domEl or
// normalized text (strings).
type domEl struct {
	tag      string
	attrs    map[string]string
	children []any
}

// FromHTML parses HTML into an estimated-layout accessibility tree.
//
// The layout is a greedy word-wrap estimate — good enough for Screen2x,
// which keys on structure (roles, hierarchy, relative box sizes) rather
// than exact geometry.
func FromHTML(htmlText string, viewportW int) *PageTree {
	root := parseDOM(htmlText)
	body := root
	for _, c := range root.children {
		if el, ok := c.(*domEl); ok && el.tag == "body" {
			body = el
			break
		}
	}

	l := &htmlLayouter{pageH: 0, viewportW: viewportW}
	out := l.layout(body, 0, 0, float64(viewportW), 16.0, false, nil)
	out.Role = "rootWebArea"
	out.Box = [4]float64{0, 0, float64(viewportW), max(l.pageH, 600)}
	return New(out, viewportW, int(out.Box[3]))
}

// parseDOM converts an HTML document into a small tolerant DOM tree,
// normalizing whitespace in text nodes like browsers' display rendering.
func parseDOM(text string) *domEl {
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		// html.Parse is tolerant and never fails on content; a failure
		// here means a broken reader, so degrade to an empty document.
		return &domEl{tag: "html"}
	}
	root := &domEl{tag: "html", attrs: map[string]string{}}
	convert(doc, root)
	return root
}

// convert appends n's element/text children (recursively) to parent.
func convert(n *html.Node, parent *domEl) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if t := strings.Join(strings.Fields(c.Data), " "); t != "" {
				parent.children = append(parent.children, t)
			}
		case html.ElementNode:
			el := &domEl{tag: strings.ToLower(c.Data), attrs: map[string]string{}}
			for _, a := range c.Attr {
				el.attrs[strings.ToLower(a.Key)] = a.Val
			}
			parent.children = append(parent.children, el)
			convert(c, el)
		case html.CommentNode:
			// dropped
		}
	}
}

type htmlLayouter struct {
	pageH     float64
	viewportW int
}

// inlineText collects the text of a node's inline descendants (used when
// a child is not itself laid out as a block).
func (l *htmlLayouter) inlineText(node *domEl) string {
	var parts []string
	for _, child := range node.children {
		switch c := child.(type) {
		case string:
			parts = append(parts, c)
		case *domEl:
			if c.tag == "br" {
				parts = append(parts, " ")
			} else if c.tag != "script" && c.tag != "style" {
				if inner := l.inlineText(c); inner != "" {
					parts = append(parts, inner)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

func (l *htmlLayouter) layout(node *domEl, x, y, w, font float64, bold bool, level *int) *Node {
	tag := node.tag
	role := roleFor[tag]
	if role == "" {
		role = "genericContainer"
	}
	if f, ok := fontFor[tag]; ok {
		font = f
	}
	if boldTags[tag] || strings.Contains(node.attrs["style"], "bold") {
		bold = true
	}
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		if d, err := strconv.Atoi(tag[1:]); err == nil {
			level = I(d)
		}
	}
	indent := indentFor[tag]
	x += indent
	w -= indent * 2

	out := &Node{Role: role, FontSize: F64(font)}
	if bold {
		out.FontWeight = F64(700.0)
	}
	out.Level = level

	switch tag {
	case "script", "style", "meta", "link", "head", "title":
		return out
	}

	if tag == "img" {
		label := node.attrs["alt"]
		if label == "" {
			src := node.attrs["src"]
			if src != "" {
				if i := strings.LastIndex(src, "/"); i >= 0 {
					src = src[i+1:]
				}
				label = "image " + src
			} else {
				label = "image"
			}
		}
		iw, ih := attrDim(node.attrs, "width", 320), attrDim(node.attrs, "height", 240)
		box := [4]float64{x, y, x + min(float64(iw), w), y + min(float64(ih), 640)}
		out.Role, out.Text, out.Box = "image", label, box
		l.pageH = max(l.pageH, box[3])
		return out
	}

	var blockKids []*domEl
	// Inline segments: runs of text and inline semantic elements (a,
	// strong, em, code, ...). Plain segments become staticText nodes;
	// element segments become link/strong/... nodes wrapping one
	// staticText child — the structure Chrome's AX trees carry and
	// Screen2x's chrome_role input expects. Generic inline wrappers
	// (span, u, sub, ...) fold into the adjacent plain text.
	type seg struct {
		el   *domEl // nil = plain text
		text string
	}
	var segs []seg
	addSeg := func(el *domEl, text string) {
		if strings.TrimSpace(text) != "" {
			segs = append(segs, seg{el, text})
		}
	}
	for _, child := range node.children {
		switch c := child.(type) {
		case string:
			addSeg(nil, c)
		case *domEl:
			switch c.tag {
			case "script", "style", "head", "title":
				continue
			}
			if inlineSemantic[c.tag] {
				addSeg(c, l.inlineText(c))
			} else if roleFor[c.tag] != "" || indentFor[c.tag] != 0 {
				blockKids = append(blockKids, c)
			} else if inner := l.inlineText(c); inner != "" {
				addSeg(nil, inner)
			}
		}
	}

	cursor := y
	if len(segs) > 0 {
		// word-wrap across segments; each word remembers its segment
		// so consecutive same-segment words on a line merge into one
		// node
		type word struct {
			text string
			seg  int // -1 = plain
		}
		var words []word
		for si, s := range segs {
			idx := si
			if s.el == nil {
				idx = -1
			}
			for _, wd := range strings.Fields(s.text) {
				words = append(words, word{wd, idx})
			}
		}
		maxChars := max(4, int(w/(font*0.52)))
		type line struct{ words []word }
		var lines []line
		var cur []word
		curLen := 0
		for _, wd := range words {
			add := len([]rune(wd.text))
			if len(cur) > 0 {
				add++
			}
			if curLen+add > maxChars && len(cur) > 0 {
				lines = append(lines, line{cur})
				cur, curLen = nil, 0
				add = len([]rune(wd.text))
			}
			cur = append(cur, wd)
			curLen += add
		}
		if len(cur) > 0 {
			lines = append(lines, line{cur})
		}

		lineH := font * 1.45
		for _, ln := range lines {
			i := 0
			for i < len(ln.words) {
				segIdx := ln.words[i].seg
				j := i
				for j < len(ln.words) && ln.words[j].seg == segIdx {
					j++
				}
				var parts []string
				for k := i; k < j; k++ {
					parts = append(parts, ln.words[k].text)
				}
				text := strings.Join(parts, " ")
				tw := min(w, float64(len([]rune(text)))*font*0.52)
				box := [4]float64{x, cursor, x + max(tw, 8), cursor + lineH}
				if segIdx < 0 {
					out.Children = append(out.Children, &Node{
						Role:       "staticText",
						Text:       text,
						Box:        box,
						FontSize:   F64(font),
						FontWeight: out.FontWeight,
					})
				} else {
					role := roleFor[segs[segIdx].el.tag]
					if role == "" {
						role = "genericContainer"
					}
					inline := &Node{Role: role, Box: box, FontSize: F64(font), FontWeight: out.FontWeight}
					inline.Children = append(inline.Children, &Node{
						Role:       "staticText",
						Text:       text,
						Box:        box,
						FontSize:   F64(font),
						FontWeight: out.FontWeight,
					})
					out.Children = append(out.Children, inline)
				}
				i = j
			}
			cursor += lineH
		}
	}
	for _, kid := range blockKids {
		sub := l.layout(kid, x, cursor, w, font, bold, level)
		if sub.Box != ([4]float64{}) {
			cursor = max(cursor, sub.Box[3]+8)
		}
		out.Children = append(out.Children, sub)
	}

	contentBottom := cursor
	box := [4]float64{x, y, x + w, max(contentBottom, y+1)}
	if len(out.Children) > 0 {
		var l0, t0, r0, b0 float64
		first := true
		for _, c := range out.Children {
			if c.Box == ([4]float64{}) {
				continue
			}
			if first {
				l0, t0, r0, b0 = c.Box[0], c.Box[1], c.Box[2], c.Box[3]
				first = false
				continue
			}
			l0, t0 = min(l0, c.Box[0]), min(t0, c.Box[1])
			r0, b0 = max(r0, c.Box[2]), max(b0, c.Box[3])
		}
		if !first {
			box = [4]float64{min(l0, x), min(t0, y), max(r0, x+8), b0 + 4}
		}
	}
	if tag == "br" {
		box = [4]float64{}
	}
	if box == ([4]float64{}) {
		box = [4]float64{x, y, x + w, max(contentBottom, y+1)}
	}
	if tag == "body" || role == "rootWebArea" {
		box = [4]float64{0, 0, float64(l.viewportW), max(l.pageH, contentBottom, 600)}
	}
	out.Box = box
	l.pageH = max(l.pageH, box[3])
	return out
}

func attrDim(attrs map[string]string, key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(attrs[key]))
	if err != nil || v == 0 {
		return fallback
	}
	return v
}
