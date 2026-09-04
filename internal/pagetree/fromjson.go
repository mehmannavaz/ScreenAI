package pagetree

import (
	"encoding/json"
	"fmt"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// FromJSON builds a tree from the project's JSON page description.
//
// Accepted shapes:
//
//	{"tree_size": [w, h], "root": {...}}           (either key works:
//	{"viewport": [w, h],  "nodes": {...}}             root/nodes, tree_size/viewport)
//	{"lines": [...], "width": w, "height": h}      (this tool's own OCR output)
//
// Node fields: role, text, box [l,t,r,b], font_size, font_weight, level,
// display, children.
func FromJSON(data []byte) (*PageTree, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("invalid JSON page tree: %w", err)
	}

	// This tool's own OCR output JSON: {"lines": [...], "width": w, ...}.
	if _, ok := probe["lines"]; ok {
		if _, ok := probe["width"]; ok {
			var ann struct {
				Lines  []pb.Line `json:"lines"`
				Width  int       `json:"width"`
				Height int       `json:"height"`
			}
			if err := json.Unmarshal(data, &ann); err != nil {
				return nil, fmt.Errorf("invalid OCR JSON: %w", err)
			}
			a := &pb.Annotation{Lines: ann.Lines, Width: ann.Width, Height: ann.Height}
			return FromOCR(a, ann.Width, ann.Height), nil
		}
	}

	var doc struct {
		TreeSize []int `json:"tree_size"`
		Viewport []int `json:"viewport"`
		Root     *Node `json:"root"`
		Nodes    *Node `json:"nodes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON page tree: %w", err)
	}

	root := doc.Root
	if root == nil {
		root = doc.Nodes
	}
	size := doc.TreeSize
	if size == nil {
		size = doc.Viewport
	}
	w, h := 1280, 800
	if len(size) >= 2 {
		w, h = size[0], size[1]
	}

	if root == nil {
		root = NewNode("rootWebArea", "", [4]float64{0, 0, float64(w), float64(h)})
	} else {
		root.Role = "rootWebArea"
	}
	if root.Box == ([4]float64{}) {
		root.Box = [4]float64{0, 0, float64(w), float64(h)}
	}
	return New(root, w, h), nil
}
