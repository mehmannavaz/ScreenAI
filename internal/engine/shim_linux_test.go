//go:build linux

package engine

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// goldenShimHex is the 448-byte ELF64 no-op shim exactly as produced by
// the reference implementation (verified loadable by dlopen against the
// real libchromescreenai.so). A divergence means the hand-assembled ELF
// changed structure.
const goldenShimHex = "7f454c4602010100000000000000000003003e0001000000000000000000000040000000000000000000000000000000000000004000380003000000000000000100000007000000000000000000000000000000000000000000000000000000c001000000000000c0010000000000000010000000000000020000000600000060010000000000006001000000000000600100000000000060000000000000006000000000000000080000000000000051e5746406000000000000000000000000000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000010000001200010058010000000000000300000000000000005f5a4e31327468726561646c6f676765723231456e61626c6554687265616465644c6f6767696e67456900010000000200000001000000000000000000000031c0c3000000000004000000000000004401000000000000050000000000000018010000000000000600000000000000e8000000000000000a000000000000002c000000000000000b00000000000000180000000000000000000000000000000000000000000000"

func TestThreadLoggerShimGolden(t *testing.T) {
	want, err := hex.DecodeString(goldenShimHex)
	if err != nil {
		t.Fatal(err)
	}
	got := buildThreadLoggerShim()
	if !bytes.Equal(got, want) {
		t.Fatalf("generated shim differs from the golden 448-byte ELF (got %d bytes)", len(got))
	}
}

func TestThreadLoggerShimStructure(t *testing.T) {
	blob := buildThreadLoggerShim()
	if string(blob[:4]) != "\x7fELF" {
		t.Fatal("bad ELF magic")
	}
	if blob[4] != 2 || blob[5] != 1 {
		t.Fatal("not ELF64 little-endian")
	}
	// e_phnum at offset 56 must be 3 (LOAD, DYNAMIC, GNU_STACK)
	if blob[56] != 3 || blob[57] != 0 {
		t.Fatalf("e_phnum = %d", blob[56])
	}
	// e_phentsize at offset 54 must be 56
	if blob[54] != 56 || blob[55] != 0 {
		t.Fatalf("e_phentsize = %d", blob[54])
	}
	// PT_GNU_STACK flags must be RW (6), not RWX — modern kernels refuse
	// objects without a non-exec stack.
	phdr3 := 0x40 + 2*56
	if blob[phdr3] != 0x51 || blob[phdr3+4] != 6 {
		t.Fatal("PT_GNU_STACK missing or executable")
	}
	// the exported symbol name must appear in .dynstr
	if !bytes.Contains(blob, []byte(threadloggerSymbol)) {
		t.Fatal("symbol name missing")
	}
}
