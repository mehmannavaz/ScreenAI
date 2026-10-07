// Package pagetree builds page trees — the input format of the real
// Screen2x main-content model.
//
// Every builder produces the same intermediate representation — a tree
// of Node values with roles, text, estimated pixel boxes and style
// attributes — which (*PageTree).Flatten converts into the exact proto
// shape Chromium sends to the library
// (services/screen_ai/proto/main_content_extractor_proto_convertor.cc):
//
//   - pre-order traversal with ids == list index,
//   - parent_id = -1 for the root, type ROOT/VIEW,
//   - BOTH pixel and normalized bounding boxes (normalized by tree dims,
//     with the root box forced to the tree dimensions),
//   - attributes: axnode_id, /extras/styles/{display,visibility,
//     font-size,font-weight,offscreen}, hierarchical_level, class_name
//     "chrome.unicorn", chrome_role, text.
//
// IMPORTANT: ExtractMainContent returns the values of the axnode_id
// attribute (the original tree node ids), NOT the proto indices —
// Flatten therefore tags every node with a stable AXNodeID (100 + index)
// and PageTree.NodeByAXNodeID maps results back.
//
// Builders:
//
//	FromOCR      VisualAnnotation   -> line/block tree
//	FromHTML     HTML document      -> estimated a11y tree
//	FromJSON     JSON page tree     -> tree (also accepts OCR JSON output)
//	FromText     plain text         -> paragraph tree
package pagetree

import (
	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// Node is one accessibility node with an estimated screen box. Boxes use
// (left, top, right, bottom) order; a zero box means "no box".
type Node struct {
	Role       string     `json:"role"`
	Text       string     `json:"text"`
	Box        [4]float64 `json:"box"`
	FontSize   *float64   `json:"font_size,omitempty"`
	FontWeight *float64   `json:"font_weight,omitempty"`
	Level      *int       `json:"level,omitempty"`
	// TextAlign is the /extras/styles/text-align value Chrome's
	// convertor emits when the AX node has one ("left", "center",
	// ...). Screen2x consumes it as a string feature.
	TextAlign string  `json:"text_align,omitempty"`
	Display   string  `json:"display,omitempty"`
	Children  []*Node `json:"children,omitempty"`

	// Assigned by Flatten:
	ID       int // proto index (position in pre-order)
	ParentID int // -1 for the root
	AXNodeID int // 100 + ID; the value Screen2x returns
}

// NewNode constructs a plain node.
func NewNode(role, text string, box [4]float64) *Node {
	return &Node{Role: role, Text: text, Box: box}
}

// F64 takes a pointer to a float (helper for optional fields).
func F64(v float64) *float64 { return &v }

// I takes a pointer to an int (helper for optional fields).
func I(v int) *int { return &v }

// Walk yields the node and all descendants in pre-order.
func (n *Node) Walk(fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// FlatNode is one flattened, proto-ready element plus bookkeeping for
// result mapping.
type FlatNode struct {
	ID            int
	ParentID      int
	ChildIDs      []int
	Type          int // pb.UIElementRoot for id 0, else pb.UIElementView
	BoxPixels     [4]int
	BoxNormalized [4]float64
	Attrs         []pb.Attr
	Node          *Node
}

// PageTree is a flattened, proto-ready tree.
type PageTree struct {
	Root    *Node
	SourceW int
	SourceH int
	TreeW   int
	TreeH   int
	Nodes   []FlatNode
	byAX    map[int]*Node
}

// New flattens root into a proto-ready PageTree. sourceW/sourceH are the
// dimensions of the original input (used when no node has a real box).
func New(root *Node, sourceW, sourceH int) *PageTree {
	t := &PageTree{Root: root, SourceW: sourceW, SourceH: sourceH}
	t.flatten()
	return t
}

func (t *PageTree) flatten() {
	// Pre-order with ids == list index. Chrome's SnapshotToViewHierarchy
	// guarantees exactly that, and the library rejects the tree
	// otherwise.
	order := []*Node{}
	var walk func(n *Node, parent int)
	walk = func(n *Node, parent int) {
		n.ID = len(order)
		n.ParentID = parent
		if n.ParentID == -1 && n.ID != 0 {
			n.ParentID = 0
		}
		n.AXNodeID = 100 + n.ID
		order = append(order, n)
		for _, c := range n.Children {
			walk(c, n.ID)
		}
	}
	walk(t.Root, -1)

	treeW, treeH := 0.0, 0.0
	for _, n := range order {
		treeW = max(treeW, n.Box[2])
		treeH = max(treeH, n.Box[3])
	}
	if treeW <= 0 || treeH <= 0 {
		treeW, treeH = float64(firstNonZero(t.SourceW, 1)), float64(firstNonZero(t.SourceH, 1))
	}
	t.TreeW, t.TreeH = int(treeW), int(treeH)

	// The root box is forced to the tree dimensions (Chromium behaviour).
	t.Root.Box = [4]float64{t.Root.Box[0], t.Root.Box[1], treeW, treeH}

	t.Nodes = make([]FlatNode, 0, len(order))
	t.byAX = make(map[int]*Node, len(order))
	for _, n := range order {
		t.byAX[n.AXNodeID] = n
		l, tp, r, b := n.Box[0], n.Box[1], n.Box[2], n.Box[3]
		attrs := []pb.Attr{
			pb.Int("axnode_id", n.AXNodeID),
			pb.String("/extras/styles/display", displayOf(n)),
			pb.Bool("/extras/styles/visibility", true),
		}
		if n.FontSize != nil {
			attrs = append(attrs, pb.Float("/extras/styles/font-size", *n.FontSize))
		}
		if n.FontWeight != nil {
			attrs = append(attrs, pb.Float("/extras/styles/font-weight", *n.FontWeight))
		}
		if n.Level != nil {
			attrs = append(attrs, pb.Int("hierarchical_level", *n.Level))
		}
		if n.TextAlign != "" {
			attrs = append(attrs, pb.String("/extras/styles/text-align", n.TextAlign))
		}
		attrs = append(attrs,
			pb.String("class_name", "chrome.unicorn"),
			pb.String("chrome_role", n.Role),
			pb.String("text", n.Text),
			pb.Bool("/extras/styles/offscreen", false),
		)
		t.Nodes = append(t.Nodes, FlatNode{
			ID:            n.ID,
			ParentID:      parentIDOf(n),
			ChildIDs:      childIDsOf(n),
			Type:          pb.UIElementView,
			BoxPixels:     [4]int{int(l), int(tp), int(r), int(b)},
			BoxNormalized: [4]float64{l / treeW, tp / treeH, r / treeW, b / treeH},
			Attrs:         attrs,
			Node:          n,
		})
	}
	if len(t.Nodes) > 0 {
		t.Nodes[0].Type = pb.UIElementRoot
		t.Nodes[0].ParentID = -1
	}
}

func displayOf(n *Node) string {
	if n.Display == "" {
		return "block"
	}
	return n.Display
}

func parentIDOf(n *Node) int {
	if n.ID == 0 {
		return -1
	}
	return n.ParentID
}

func childIDsOf(n *Node) []int {
	ids := make([]int, 0, len(n.Children))
	for _, c := range n.Children {
		ids = append(ids, c.ID)
	}
	return ids
}

// NodeByAXNodeID maps a Screen2x result id back to its node.
func (t *PageTree) NodeByAXNodeID(axnodeID int) *Node {
	return t.byAX[axnodeID]
}

// Serialize returns the screenai.ViewHierarchy proto bytes.
func (t *PageTree) Serialize() []byte {
	elements := make([]pb.Element, 0, len(t.Nodes))
	for i := range t.Nodes {
		fn := &t.Nodes[i]
		elements = append(elements, pb.Element{
			ID:       fn.ID,
			Type:     fn.Type,
			ParentID: fn.ParentID,
			ChildIDs: fn.ChildIDs,
			// proto order is (top, left, bottom, right)
			BBoxNormalized: [4]float64{fn.BoxNormalized[1], fn.BoxNormalized[0], fn.BoxNormalized[3], fn.BoxNormalized[2]},
			BBoxPixels:     [4]int{fn.BoxPixels[1], fn.BoxPixels[0], fn.BoxPixels[3], fn.BoxPixels[2]},
			Attrs:          fn.Attrs,
		})
	}
	return pb.EncodeViewHierarchy(elements)
}

// ToJSON returns a plain-JSON view of the tree (for reports and
// round-trips).
func (t *PageTree) ToJSON() map[string]any {
	return map[string]any{
		"tree_size": [2]int{t.TreeW, t.TreeH},
		"nodes":     t.Root,
	}
}

// SubtreeText returns own text for leaves, joined descendant text for
// containers.
func SubtreeText(n *Node) string {
	if len(n.Children) == 0 {
		return n.Text
	}
	parts := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		if s := SubtreeText(c); s != "" {
			parts = append(parts, s)
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// ComputeMainNode implements Chrome's ComputeMainNode: the lowest common
// ancestor of the first and last selected nodes
// (services/screen_ai/screen_ai_service_impl.cc). It returns false when
// there is nothing selected.
func (t *PageTree) ComputeMainNode(contentProtoIDs []int) (int, bool) {
	if len(contentProtoIDs) == 0 {
		return 0, false
	}
	node := func(id int) *Node {
		if id >= 0 && id < len(t.Nodes) {
			return t.Nodes[id].Node
		}
		return nil
	}
	ancestors := func(start int) []int {
		chain := []int{}
		cur := node(start)
		for cur != nil {
			chain = append(chain, cur.ID)
			cur = node(cur.ParentID)
		}
		return chain // [self, parent, ..., root]
	}
	front := ancestors(contentProtoIDs[0])
	back := ancestors(contentProtoIDs[len(contentProtoIDs)-1])
	backSet := map[int]bool{}
	for _, id := range back {
		backSet[id] = true
	}
	for _, id := range front { // deepest-first
		if backSet[id] {
			return id, true
		}
	}
	return 0, true // the root, defensively
}

func firstNonZero(v, fallback int) int {
	if v != 0 {
		return v
	}
	return fallback
}
