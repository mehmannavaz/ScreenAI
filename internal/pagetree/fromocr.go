package pagetree

import (
	"sort"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// FromOCR groups OCR lines into blocks/paragraphs, then into a page tree.
// The tree is what the Screen2x model sees when judging "main content" of
// an image.
func FromOCR(annotation *pb.Annotation, imageW, imageH int) *PageTree {
	root := NewNode("rootWebArea", "", [4]float64{0, 0, float64(imageW), float64(imageH)})

	// Group lines by OCR block id, keeping a stable order.
	blocks := map[int][]*pb.Line{}
	blockOrder := []int{}
	for i := range annotation.Lines {
		ln := &annotation.Lines[i]
		if _, ok := blocks[ln.BlockID]; !ok {
			blockOrder = append(blockOrder, ln.BlockID)
		}
		blocks[ln.BlockID] = append(blocks[ln.BlockID], ln)
	}
	sort.Ints(blockOrder)

	medianH := medianLineHeight(annotation.Lines)
	for _, bid := range blockOrder {
		blockLines := blocks[bid]
		x0, y0, x1, y1 := bboxOfLines(blockLines)
		para := NewNode("paragraph", "", [4]float64{x0, y0, x1, y1})
		for _, ln := range blockLines {
			b := ln.Box
			if b == nil {
				continue
			}
			box := [4]float64{float64(b.X), float64(b.Y), float64(b.X + b.Width), float64(b.Y + b.Height)}
			fontPx := max(8.0, float64(round64(float64(b.Height)*0.78)))
			weight := 400.0
			if float64(b.Height) >= 1.6*medianH {
				weight = 700.0
			}
			para.Children = append(para.Children, &Node{
				Role:       "staticText",
				Text:       ln.Text,
				Box:        box,
				FontSize:   F64(fontPx),
				FontWeight: F64(weight),
			})
		}
		root.Children = append(root.Children, para)
	}
	return New(root, imageW, imageH)
}

func medianLineHeight(lines []pb.Line) float64 {
	if len(lines) == 0 {
		return 16.0
	}
	heights := make([]float64, 0, len(lines))
	for i := range lines {
		if lines[i].Box != nil {
			heights = append(heights, float64(lines[i].Box.Height))
		}
	}
	if len(heights) == 0 {
		return 16.0
	}
	sort.Float64s(heights)
	h := heights[len(heights)/2]
	if h <= 0 {
		return 16.0
	}
	return h
}

func bboxOfLines(lines []*pb.Line) (x0, y0, x1, y1 float64) {
	first := true
	for _, ln := range lines {
		b := ln.Box
		if b == nil {
			continue
		}
		l, t := float64(b.X), float64(b.Y)
		r, bo := float64(b.X+b.Width), float64(b.Y+b.Height)
		if first {
			x0, y0, x1, y1 = l, t, r, bo
			first = false
			continue
		}
		x0, y0 = min(x0, l), min(y0, t)
		x1, y1 = max(x1, r), max(y1, bo)
	}
	return
}

func round64(v float64) int {
	if v < 0 {
		return int(v - 0.5)
	}
	return int(v + 0.5)
}
