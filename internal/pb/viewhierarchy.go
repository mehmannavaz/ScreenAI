package pb

// screenai.UiElementType values used by ViewHierarchy elements.
const (
	UIElementRoot = 1 // ROOT
	UIElementView = 3 // VIEW
)

// AttrKind discriminates which value of an Attr is set.
type AttrKind uint8

// Attribute value kinds.
const (
	AttrBool AttrKind = iota
	AttrInt
	AttrString
	AttrFloat
)

// Attr is a screenai.UiElementAttribute: a name plus exactly one typed
// value. Construct them with Bool, Int, String and Float.
type Attr struct {
	Name string
	Kind AttrKind

	b bool
	i int64
	s string
	f float64
}

// Bool builds a boolean attribute.
func Bool(name string, v bool) Attr { return Attr{Name: name, Kind: AttrBool, b: v} }

// Int builds an int64 attribute.
func Int(name string, v int) Attr { return Attr{Name: name, Kind: AttrInt, i: int64(v)} }

// String builds a string attribute.
func String(name, v string) Attr { return Attr{Name: name, Kind: AttrString, s: v} }

// Float builds a float attribute (serialized as 32-bit).
func Float(name string, v float64) Attr { return Attr{Name: name, Kind: AttrFloat, f: v} }

// Element is one screenai.UiElement of a ViewHierarchy. BBoxPixels and
// BBoxNormalized use the proto order (top, left, bottom, right); the tree
// package converts from its own (left, top, right, bottom) layout.
type Element struct {
	ID             int
	Type           int // UIElementRoot for the first element, else UIElementView
	ParentID       int // -1 for the root
	ChildIDs       []int
	BBoxNormalized [4]float64 // top, left, bottom, right in [0, 1]
	BBoxPixels     [4]int     // top, left, bottom, right
	Attrs          []Attr
}

// EncodeViewHierarchy serializes a screenai.ViewHierarchy from elements.
//
// It mirrors Chromium's SnapshotToViewHierarchy
// (services/screen_ai/proto/main_content_extractor_proto_convertor.cc):
// elements are given in pre-order with ids equal to their index, the
// root's parent_id is -1, and every element carries both pixel and
// normalized bounding boxes.
func EncodeViewHierarchy(elements []Element) []byte {
	var payload []byte
	for i := range elements {
		el := &elements[i]
		var msg []byte
		msg = AppendVarintField(msg, 1, uint64(el.ID))
		msg = AppendVarintField(msg, 2, uint64(el.Type))
		msg = AppendVarintField(msg, 3, uint64(el.ParentID))
		if len(el.ChildIDs) > 0 {
			msg = AppendPackedInt32Field(msg, 4, el.ChildIDs)
		}
		n := el.BBoxNormalized
		msg = AppendMessageField(msg, 6, encodeBBox(n[0], n[1], n[2], n[3]))
		for _, attr := range el.Attrs {
			msg = AppendMessageField(msg, 7, encodeAttr(attr))
		}
		p := el.BBoxPixels
		msg = AppendMessageField(msg, 8, encodeBBoxPixels(p[0], p[1], p[2], p[3]))
		payload = AppendMessageField(payload, 2, msg) // ui_elements
	}
	return payload
}

func encodeBBox(top, left, bottom, right float64) []byte {
	var b []byte
	b = AppendFloatField(b, 1, top)
	b = AppendFloatField(b, 2, left)
	b = AppendFloatField(b, 3, bottom)
	b = AppendFloatField(b, 4, right)
	return b
}

func encodeBBoxPixels(top, left, bottom, right int) []byte {
	var b []byte
	b = AppendVarintField(b, 1, uint64(top))
	b = AppendVarintField(b, 2, uint64(left))
	b = AppendVarintField(b, 3, uint64(bottom))
	b = AppendVarintField(b, 4, uint64(right))
	return b
}

func encodeAttr(a Attr) []byte {
	var b []byte
	b = AppendStringField(b, 1, a.Name)
	switch a.Kind {
	case AttrBool:
		b = AppendBoolField(b, 2, a.b)
	case AttrInt:
		b = AppendVarintField(b, 3, uint64(a.i))
	case AttrString:
		b = AppendStringField(b, 4, a.s)
	case AttrFloat:
		b = AppendFloatField(b, 5, a.f)
	}
	return b
}
