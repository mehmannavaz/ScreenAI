// Package selftest verifies the shipped binary works on the current
// machine: image codecs against pixel-exact fixtures, the protobuf
// codecs against golden bytes produced by the reference implementation,
// tree-flattening invariants, the Linux threadlogger shim, and (when the
// component is present) a live engine smoke test.
package selftest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"image"

	"github.com/mehmannavaz/ScreenAI/examples"
	"github.com/mehmannavaz/ScreenAI/internal/engine"
	"github.com/mehmannavaz/ScreenAI/internal/pagetree"
	"github.com/mehmannavaz/ScreenAI/internal/pb"
	"github.com/mehmannavaz/ScreenAI/internal/pipeline"
)

// goldenViewHierarchyHex is the serialized screenai.ViewHierarchy of
// examples/example_4_page_tree.json, byte-exact as produced by the
// reference implementation that was verified against the real Screen2x
// model. Any divergence means the encoder changed wire behaviour.
const goldenViewHierarchyHex = "12e9010800100118ffffffffffffffffff0122050102030b0c32140d0000000015000000001d0000803f250000803f3a0d0a0961786e6f64655f696418643a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a1a0a0b6368726f6d655f726f6c65220b726f6f74576562417265613a080a047465787422003a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420a0800100018a40d20800a128e0208011003180032140d0000000015000000001dcd331a3d250000803f3a0d0a0961786e6f64655f696418653a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000060413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a190a0b6368726f6d655f726f6c65220a6e617669676174696f6e3a1e0a04746578742216526563697065732044657373657274732041626f75743a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420908001000184020800a12a80208021003180032140dcd331a3d15000000001db5b4b43d250000803f3a0d0a0961786e6f64655f696418663a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000010423a210a1a2f6578747261732f7374796c65732f666f6e742d7765696768742d00002f443a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a150a0b6368726f6d655f726f6c65220662616e6e65723a180a0474657874221054686520536c6f77204b69746368656e3a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420a0840100018960120800a12d801080310031800220304050832140de5e4e43d150000c03d1d5a5a5a3f250000683f3a0d0a0961786e6f64655f696418673a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a130a0b6368726f6d655f726f6c6522046d61696e3a080a047465787422003a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08be01107818aa0b20880912ca0208041003180332140de5e4e43d150000c03d1d9d9c1c3e250000343f3a0d0a0961786e6f64655f696418683a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d0000f0413a210a1a2f6578747261732f7374796c65732f666f6e742d7765696768742d00002f443a160a1268696572617263686963616c5f6c6576656c18013a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a160a0b6368726f6d655f726f6c65220768656164696e673a200a04746578742218427265616420746861742074616b657320616c6c206461793a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08be01107818840220840712dc010805100318032202060732140da9a8283e150000c03d1d8e8d8d3e250000343f3a0d0a0961786e6f64655f696418693a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a180a0b6368726f6d655f726f6c6522097061726167726170683a080a047465787422003a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b089802107818d60320840712d60208061003180532140da9a8283e150000c03d1dcdcc4c3e250000343f3a0d0a0961786e6f64655f6964186a3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000088413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a190a0b6368726f6d655f726f6c65220a737461746963546578743a640a0474657874225c4c6f6e67206665726d656e746174696f6e206973206e6f7420612066617368696f6e3b2069742069732061206368656d697374727920746861742074726164657320796f75722063616c656e64617220666f7220666c61766f75722e3a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b089802107818d40220840712d40208071003180532140dd0cf4f3e150000c03d1df4f3733e250000343f3a0d0a0961786e6f64655f6964186b3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000088413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a190a0b6368726f6d655f726f6c65220a737461746963546578743a620a0474657874225a4120636f6c64206f7665726e696768742070726f6f66206c65747320656e7a796d657320736e697020737461726368657320696e746f2073756761727320746865207965617374206e6576657220636f756c642072656163682e3a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08d902107818950320840712dc010808100318032202090a32140d9796963e150000c03d1dd3d2d23e250000343f3a0d0a0961786e6f64655f6964186c3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a180a0b6368726f6d655f726f6c6522097061726167726170683a080a047465787422003a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08f403107818bc0520840712eb0208091003180832140d9796963e150000c03d1da9a8a83e250000343f3a0d0a0961786e6f64655f6964186d3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000088413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a190a0b6368726f6d655f726f6c65220a737461746963546578743a790a04746578742271537461727420746865206c657661696e20467269646179206e696768742c20666f6c6420697420666f75722074696d65732053617475726461792c20616e642062616b652053756e646179206d6f726e696e67207768696c652074686520686f757365207374696c6c20736c656570732e3a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08f403107818b00420840712cd02080a1003180832140d2a2aaa3e150000c03d1d3c3cbc3e250000343f3a0d0a0961786e6f64655f6964186e3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000088413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a190a0b6368726f6d655f726f6c65220a737461746963546578743a5b0a04746578742253546865207265776172642069732061206372756d622074686174207374617973206d6f69737420666f72206461797320616e64206120637275737420746861742073696e677320617320697420636f6f6c732e3a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08b504107818f104208407129d02080b1003180032140de5e4e43d1500003c3f1dfdfc7c3e250000783f3a0d0a0961786e6f64655f6964186f3a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000050413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a1c0a0b6368726f6d655f726f6c65220d636f6d706c656d656e746172793a270a0474657874221f4164202d205374616e64206d697865722073616c65202d20333025206f66663a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420c08be0110ac0718a40320d809129802080c1003180032140de2e1613f15000000001df1f0703f250000803f3a0d0a0961786e6f64655f696418703a1f0a162f6578747261732f7374796c65732f646973706c61792205626c6f636b3a1d0a192f6578747261732f7374796c65732f7669736962696c69747910013a1f0a182f6578747261732f7374796c65732f666f6e742d73697a652d000040413a1c0a0a636c6173735f6e616d65220e6368726f6d652e756e69636f726e3a1a0a0b6368726f6d655f726f6c65220b636f6e74656e74696e666f3a250a0474657874221d54686520536c6f77204b69746368656e203230323620436f6e746163743a1c0a182f6578747261732f7374796c65732f6f666673637265656e1000420b08dc0b100018c00c20800a"

// Result carries the outcome of one check.
type Result struct {
	Name string
	Err  error
}

// Run executes the full selftest. When componentDir refers to a real
// component (or auto-detection finds one), a live engine smoke test
// runs on top of the codec checks.
func Run(componentDir string) []Result {
	var results []Result
	check := func(name string, fn func() error) {
		results = append(results, Result{name, fn()})
	}

	check("tga-uncompressed-24", testTGAUncompressed)
	check("tga-rle-32", testTGARLE)
	check("tga-palette-8", testTGAPalette)
	check("ppm-binary-p6", testPPM6)
	check("ppm-ascii-p2", testPPM2)
	check("png-roundtrip", testPNGRoundtrip)
	check("downscale-box", testDownscale)
	check("proto-annotation-decode", testAnnotationDecode)
	check("proto-varint-roundtrip", testVarintRoundtrip)
	check("viewhierarchy-golden", testViewHierarchyGolden)
	check("tree-flatten-invariants", testFlattenInvariants)
	check("tree-fromhtml", testFromHTML)
	check("classify-sniffing", testClassify)
	if _, err := engine.Locate(componentDir); err == nil {
		check("engine-smoke", func() error { return engineSmoke(componentDir) })
	} else {
		fmt.Println("note: component not found — skipping the live engine smoke test")
	}
	return results
}

// PrintAndReport prints the results and returns a non-nil error when
// any check failed.
func PrintAndReport(results []Result) error {
	failed := 0
	for _, r := range results {
		status := "ok"
		if r.Err != nil {
			status = "FAIL: " + r.Err.Error()
			failed++
		}
		fmt.Printf("  [%-4s] %s\n", status, r.Name)
	}
	fmt.Printf("selftest: %d ok, %d failed\n", len(results)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("selftest: %d of %d checks failed", failed, len(results))
	}
	return nil
}

func testViewHierarchyGolden() error {
	want, err := hex.DecodeString(goldenViewHierarchyHex)
	if err != nil {
		return err
	}
	tree, err := pagetree.FromJSON(examples.Map()["example_4_page_tree.json"])
	if err != nil {
		return err
	}
	got := tree.Serialize()
	if !bytes.Equal(got, want) {
		return fmt.Errorf("serialized ViewHierarchy differs from the golden bytes (got %d bytes, want %d)", len(got), len(want))
	}
	return nil
}

func testFlattenInvariants() error {
	tree, err := pagetree.FromJSON(examples.Map()["example_4_page_tree.json"])
	if err != nil {
		return err
	}
	for i := range tree.Nodes {
		n := tree.Nodes[i]
		if n.ID != i {
			return fmt.Errorf("node id %d != index %d — the library rejects such trees", n.ID, i)
		}
		if n.Node.AXNodeID != 100+i {
			return fmt.Errorf("axnode_id %d != 100+%d", n.Node.AXNodeID, i)
		}
		if i == 0 {
			if n.ParentID != -1 || n.Type != pb.UIElementRoot {
				return fmt.Errorf("root must have parent -1 and type ROOT")
			}
		} else if n.ParentID < 0 || n.ParentID >= i {
			return fmt.Errorf("node %d has invalid parent %d", i, n.ParentID)
		}
	}
	// every child listed must point back at its parent
	for i := range tree.Nodes {
		for _, cid := range tree.Nodes[i].ChildIDs {
			if tree.Nodes[cid].ParentID != i {
				return fmt.Errorf("child %d of %d does not point back", cid, i)
			}
		}
	}
	return nil
}

func testFromHTML() error {
	tree := pagetree.FromHTML(string(examples.Map()["example_3_article.html"]), 1280)
	if len(tree.Nodes) < 10 {
		return fmt.Errorf("HTML tree has only %d nodes", len(tree.Nodes))
	}
	heading := false
	for i := range tree.Nodes {
		if tree.Nodes[i].Node.Role == "heading" {
			heading = true
			break
		}
	}
	if !heading {
		return fmt.Errorf("HTML tree has no heading node")
	}
	return testFlattenOf(tree)
}

func testFlattenOf(tree *pagetree.PageTree) error {
	for i := range tree.Nodes {
		n := tree.Nodes[i]
		if n.ID != i || n.Node.AXNodeID != 100+i {
			return fmt.Errorf("flatten invariants violated at %d", i)
		}
	}
	return nil
}

func testClassify() error {
	png := examples.SampleNewsPNG()
	inp, err := pipeline.Classify(png, "x.png", "", "x.png")
	if err != nil || inp.Kind != pipeline.KindImage {
		return fmt.Errorf("PNG not classified as image: %v", err)
	}
	inp, err = pipeline.Classify([]byte("<!DOCTYPE html><html><body><p>hi</p></body></html>"), "x.txt", "", "x")
	if err != nil || inp.Kind != pipeline.KindPage {
		return fmt.Errorf("HTML not classified as page: %v", err)
	}
	inp, err = pipeline.Classify([]byte(`{"tree_size":[2,2],"root":{}}`), "x.bin", "", "x")
	if err != nil || inp.Kind != pipeline.KindTree {
		return fmt.Errorf("JSON not classified as tree: %v", err)
	}
	inp, err = pipeline.Classify([]byte("plain text here"), "x.dat", "", "x")
	if err != nil || inp.Kind != pipeline.KindText {
		return fmt.Errorf("text not classified: %v", err)
	}
	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = byte(i * 7)
	}
	if _, err := pipeline.Classify(garbage, "x.bin", "", "x"); err == nil {
		return fmt.Errorf("binary garbage must be rejected")
	}
	return nil
}

func testVarintRoundtrip() error {
	for _, v := range []uint64{0, 1, 127, 128, 300, 1 << 20, 1<<32 - 1, 1 << 63} {
		got, _, err := pb.ReadVarint(pb.AppendVarint(nil, v), 0)
		if err != nil || got != v {
			return fmt.Errorf("varint roundtrip failed for %d: %d %v", v, got, err)
		}
	}
	return nil
}

func testAnnotationDecode() error {
	// A VisualAnnotation with one line: text "Hi", box (1,2,3x4),
	// confidence 0.5, language "en", block 1 — hand-encoded.
	var line []byte
	line = pb.AppendStringField(line, 3, "Hi") // utf8_string
	var box []byte
	box = pb.AppendVarintField(box, 1, 1)      // x
	box = pb.AppendVarintField(box, 2, 2)      // y
	box = pb.AppendVarintField(box, 3, 3)      // width
	box = pb.AppendVarintField(box, 4, 4)      // height
	line = pb.AppendBytesField(line, 2, box)   // bounding_box
	line = pb.AppendVarintField(line, 5, 1)    // block_id
	line = pb.AppendFloatField(line, 10, 0.5)  // confidence
	line = pb.AppendStringField(line, 4, "en") // language

	ann := []byte{}
	ann = pb.AppendBytesField(ann, 2, line)

	got, err := pb.DecodeAnnotation(ann)
	if err != nil {
		return err
	}
	if len(got.Lines) != 1 {
		return fmt.Errorf("want 1 line, got %d", len(got.Lines))
	}
	ln := got.Lines[0]
	if ln.Text != "Hi" || ln.BlockID != 1 || ln.Language != "en" {
		return fmt.Errorf("line fields wrong: %+v", ln)
	}
	if ln.Box == nil || ln.Box.X != 1 || ln.Box.Y != 2 || ln.Box.Width != 3 || ln.Box.Height != 4 {
		return fmt.Errorf("line box wrong: %+v", ln.Box)
	}
	if ln.Confidence != 0.5 {
		return fmt.Errorf("confidence wrong: %v", ln.Confidence)
	}
	if got.PlainText() != "Hi" {
		return fmt.Errorf("plain text wrong: %q", got.PlainText())
	}
	return nil
}

func engineSmoke(componentDir string) error {
	eng, err := engine.New(engine.Options{ComponentDir: componentDir})
	if err != nil {
		return err
	}
	defer eng.Close()
	if err := eng.InitOCR(); err != nil {
		return err
	}
	major, _ := eng.Version()
	if major == 0 {
		return fmt.Errorf("library reported version 0")
	}
	img := examples.SampleNewsImage()
	if img == nil {
		return fmt.Errorf("bundled sample missing")
	}
	ann, err := eng.PerformOCR(img.Bounds().Dx(), img.Bounds().Dy(), img.Pix)
	if err != nil {
		return err
	}
	if len(ann.Lines) < 10 {
		return fmt.Errorf("sample OCR found only %d lines", len(ann.Lines))
	}
	fmt.Printf("         (engine v%s, sample OCR: %d lines / %d words)\n",
		eng.VersionString(), len(ann.Lines), ann.WordCount())
	return nil
}

// helpers for the codec fixture tests
func px(img *image.RGBA, x, y int) (uint8, uint8, uint8, uint8) {
	i := y*img.Stride + x*4
	return img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]
}

func wantPixel(t string, img *image.RGBA, x, y int, wantR, wantG, wantB, wantA uint8) error {
	r, g, b, a := px(img, x, y)
	if r != wantR || g != wantG || b != wantB || a != wantA {
		return fmt.Errorf("%s: pixel (%d,%d) = rgba(%d,%d,%d,%d), want rgba(%d,%d,%d,%d)", t, x, y, r, g, b, a, wantR, wantG, wantB, wantA)
	}
	return nil
}
