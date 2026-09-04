package pagetree

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

const sampleTreeJSON = `{
 "tree_size": [1280, 1700],
 "root": {
  "role": "rootWebArea", "text": "", "box": [0, 0, 1280, 1700],
  "children": [
   {"role": "navigation", "text": "Recipes About", "box": [0, 0, 1280, 64], "font_size": 14},
   {"role": "main", "text": "", "box": [120, 190, 1160, 1450], "children": [
    {"role": "heading", "text": "Bread", "box": [120, 190, 900, 260],
     "font_size": 30, "font_weight": 700, "level": 1},
    {"role": "staticText", "text": "Long fermentation.", "box": [120, 280, 900, 340], "font_size": 17}]},
   {"role": "contentinfo", "text": "footer", "box": [0, 1500, 1280, 1600]}
  ]
 }
}`

func TestFlattenInvariants(t *testing.T) {
	tree, err := FromJSON([]byte(sampleTreeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Nodes) != 6 {
		t.Fatalf("want 6 nodes, got %d", len(tree.Nodes))
	}
	for i, fn := range tree.Nodes {
		if fn.ID != i {
			t.Fatalf("node id %d != index %d — the library rejects such trees", fn.ID, i)
		}
		if fn.Node.AXNodeID != 100+i {
			t.Fatalf("axnode_id %d != 100+%d", fn.Node.AXNodeID, i)
		}
		if i == 0 {
			if fn.ParentID != -1 || fn.Type != pb.UIElementRoot {
				t.Fatal("root must have parent -1 and type ROOT")
			}
		} else if fn.ParentID < 0 || fn.ParentID >= i {
			t.Fatalf("node %d has invalid parent %d", i, fn.ParentID)
		}
		for _, cid := range fn.ChildIDs {
			if tree.Nodes[cid].ParentID != i {
				t.Fatalf("child %d of %d does not point back", cid, i)
			}
		}
	}
	if tree.TreeW != 1280 || tree.TreeH != 1700 {
		t.Fatalf("tree dims %dx%d", tree.TreeW, tree.TreeH)
	}
	// NodeByAXNodeID maps back.
	if tree.NodeByAXNodeID(103) == nil || tree.NodeByAXNodeID(103).Role != "heading" {
		t.Fatal("NodeByAXNodeID broken")
	}
}

func TestSerializeMatchesChromiumShape(t *testing.T) {
	tree, err := FromJSON([]byte(sampleTreeJSON))
	if err != nil {
		t.Fatal(err)
	}
	data := tree.Serialize()
	if len(data) == 0 {
		t.Fatal("empty serialization")
	}
	// Walk the top-level fields: everything must be field 2 (ui_elements).
	count := 0
	err = pb.ForEachField(data, func(f pb.Field) bool {
		if f.Num != 2 || f.Wire != pb.WireBytes {
			t.Fatalf("unexpected top-level field %d wire %d", f.Num, f.Wire)
		}
		count++
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != len(tree.Nodes) {
		t.Fatalf("want %d ui_elements, got %d", len(tree.Nodes), count)
	}
}

func TestFromJSONOCROutputShape(t *testing.T) {
	doc := `{"lines": [{"text": "hi", "box": {"x": 0, "y": 0, "width": 10, "height": 12, "angle": 0.0}, "confidence": 0.9, "block_id": 0}], "width": 100, "height": 50}`
	tree, err := FromJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if tree.TreeW != 100 || tree.TreeH != 50 {
		t.Fatalf("dims %dx%d", tree.TreeW, tree.TreeH)
	}
	if len(tree.Nodes) != 3 { // root + paragraph + line
		t.Fatalf("want 3 nodes, got %d", len(tree.Nodes))
	}
}

func TestFromText(t *testing.T) {
	text := "First paragraph here.\n\nSecond paragraph follows."
	tree := FromText(text, 800)
	if len(tree.Nodes) < 5 {
		t.Fatalf("too few nodes: %d", len(tree.Nodes))
	}
	joined := SubtreeText(tree.Root)
	if !strings.Contains(joined, "First paragraph here.") || !strings.Contains(joined, "Second paragraph follows.") {
		t.Fatalf("text lost: %q", joined)
	}
	for i, fn := range tree.Nodes {
		if fn.ID != i {
			t.Fatalf("flatten invariants broken at %d", i)
		}
	}
}

func TestFromHTML(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
<nav><a href="/">Home</a> <a href="/x">Guides</a></nav>
<main><article><h1>Title</h1><p>Body text long enough to wrap.</p></article></main>
<aside>Ad text here</aside><footer>Copyright</footer></body></html>`
	tree := FromHTML(html, 1280)
	if len(tree.Nodes) < 10 {
		t.Fatalf("HTML tree has only %d nodes", len(tree.Nodes))
	}
	foundHeading := false
	for i := range tree.Nodes {
		if tree.Nodes[i].Node.Role == "heading" {
			foundHeading = true
			break
		}
	}
	if !foundHeading {
		t.Fatal("no heading node")
	}
	if tree.TreeW != 1280 {
		t.Fatalf("viewport not respected: %d", tree.TreeW)
	}
	for i, fn := range tree.Nodes {
		if fn.ID != i || fn.Node.AXNodeID != 100+i {
			t.Fatalf("flatten invariants broken at %d", i)
		}
	}
}

func TestComputeMainNode(t *testing.T) {
	tree, err := FromJSON([]byte(sampleTreeJSON))
	if err != nil {
		t.Fatal(err)
	}
	// heading (index 3) and staticText (index 4) are inside main (index 2).
	main, ok := tree.ComputeMainNode([]int{3, 4})
	if !ok || main != 2 {
		t.Fatalf("LCA(3,4) = %d ok=%v, want 2", main, ok)
	}
	// navigation (1) and footer (5): LCA is the root (0).
	main, ok = tree.ComputeMainNode([]int{1, 5})
	if !ok || main != 0 {
		t.Fatalf("LCA(1,5) = %d ok=%v, want 0", main, ok)
	}
	if _, ok := tree.ComputeMainNode(nil); ok {
		t.Fatal("empty selection must report false")
	}
}

func TestNodeJSONRoundtrip(t *testing.T) {
	var doc struct {
		Root *Node `json:"root"`
	}
	if err := json.Unmarshal([]byte(`{"root":{"role":"x","text":"t","box":[1,2,3,4],"level":2,"children":[{"role":"y"}]}}`), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Root.Box != [4]float64{1, 2, 3, 4} || doc.Root.Level == nil || *doc.Root.Level != 2 {
		t.Fatalf("roundtrip wrong: %+v", doc.Root)
	}
	if len(doc.Root.Children) != 1 {
		t.Fatalf("children lost")
	}
}

func TestToJSON(t *testing.T) {
	tree, err := FromJSON([]byte(sampleTreeJSON))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(tree.ToJSON())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("tree_size")) {
		t.Fatal("ToJSON lost tree_size")
	}
}
