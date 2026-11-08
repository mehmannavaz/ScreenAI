package engine

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// TestModelFileCallbacksAreCCallable exercises the model-file callbacks
// exactly the way the component library does: C code calls the function
// pointers registered through SetFileContentFunctions.
//
// On Windows purego.NewCallback delegates to syscall.NewCallback, whose
// runtime check requires a callback with exactly one pointer-sized
// result — the modelFileCache methods used to return uint32 and void,
// which panicked at engine start with "compileCallback: expected function
// with one uintptr-sized result" before any OCR could run. The round trip
// below calls the trampolines through purego.SyscallN, so the full C-ABI
// path (arguments in registers, result in the return register) is covered
// on every platform without needing the component itself.
func TestModelFileCallbacksAreCCallable(t *testing.T) {
	dir := t.TempDir()
	c := newModelFileCache(dir, nil)

	sizePtr := purego.NewCallback(c.sizeCB)
	contentPtr := purego.NewCallback(c.contentCB)

	// A missing file reports size 0, like Chromium's own wrapper.
	missing := []byte("does_not_exist.bin\x00")
	if r, _, _ := purego.SyscallN(sizePtr, uintptr(unsafe.Pointer(&missing[0]))); uint32(r) != 0 {
		t.Fatalf("size callback for a missing file returned %d, want 0", uint32(r))
	}

	// An existing file reports its byte size.
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := []byte("model.bin\x00")
	if r, _, _ := purego.SyscallN(sizePtr, uintptr(unsafe.Pointer(&name[0]))); r != 5 {
		t.Fatalf("size callback returned %d, want 5", r)
	}

	// The content callback copies the bytes into the library buffer and
	// returns a deterministic 0 for the void C signature.
	buf := make([]byte, 5)
	if r, _, _ := purego.SyscallN(contentPtr, uintptr(unsafe.Pointer(&name[0])), 5, uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		t.Fatalf("content callback returned %d, want 0 (void C signature)", r)
	}
	if string(buf) != "hello" {
		t.Fatalf("content callback copied %q, want %q", buf, "hello")
	}

	// A buffer smaller than the file is filled without overrunning.
	short := make([]byte, 3)
	if r, _, _ := purego.SyscallN(contentPtr, uintptr(unsafe.Pointer(&name[0])), 3, uintptr(unsafe.Pointer(&short[0]))); r != 0 {
		t.Fatalf("content callback returned %d, want 0", r)
	}
	if string(short) != "hel" {
		t.Fatalf("content callback copied %q, want %q", short, "hel")
	}
}
