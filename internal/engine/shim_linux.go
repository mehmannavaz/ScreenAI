//go:build linux

package engine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// threadloggerSymbol is the mangled name of
// threadlogger::EnableThreadedLogging(int) — a symbol libchromescreenai.so
// references but which normally lives in Chrome's browser process. A no-op
// shim exporting it must be loaded (RTLD_GLOBAL) before the library.
const threadloggerSymbol = "_ZN12threadlogger21EnableThreadedLoggingEi"

// buildThreadLoggerShim hand-assembles a 448-byte ELF64 shared object
// exporting one no-op function.
//
// This keeps ScreenAI truly single-binary on Linux: instead of shipping a
// prebuilt libthreadlogger_shim.so (or requiring a C compiler), the needed
// shared object — EHDR + 3 phdrs + dynsym/dynstr/hash + code + dynamic —
// is emitted right here. PT_GNU_STACK is included as non-exec, because
// modern kernels refuse objects without it.
func buildThreadLoggerShim() []byte {
	const offPhdr = 0x40
	const phdrEnt = 56               // sizeof(Elf64_Phdr)
	offDynsym := offPhdr + 3*phdrEnt // LOAD + DYNAMIC + GNU_STACK
	dynstr := append([]byte{0}, append([]byte(threadloggerSymbol), 0)...)
	offDynstr := offDynsym + 2*24 // null symbol + the export
	offHash := offDynstr + len(dynstr)
	offCode := offHash + 20
	offDynamic := (offCode + 3 + 7) &^ 7
	total := offDynamic + 6*16
	buf := make([]byte, total)

	// ELF header.
	copy(buf[0:16], []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	binary.LittleEndian.PutUint16(buf[16:], 3)  // e_type = ET_DYN
	binary.LittleEndian.PutUint16(buf[18:], 62) // e_machine = EM_X86_64
	binary.LittleEndian.PutUint32(buf[20:], 1)  // e_version
	binary.LittleEndian.PutUint64(buf[24:], 0)  // e_entry
	binary.LittleEndian.PutUint64(buf[32:], offPhdr)
	binary.LittleEndian.PutUint16(buf[52:], 64)      // e_ehsize
	binary.LittleEndian.PutUint16(buf[54:], phdrEnt) // e_phentsize
	binary.LittleEndian.PutUint16(buf[56:], 3)       // e_phnum

	// Program headers.
	putPhdr := func(off int, ptype, flags uint32, offset, vaddr, paddr, filesz, memsz, align uint64) {
		binary.LittleEndian.PutUint32(buf[off:], ptype)
		binary.LittleEndian.PutUint32(buf[off+4:], flags)
		binary.LittleEndian.PutUint64(buf[off+8:], offset)
		binary.LittleEndian.PutUint64(buf[off+16:], vaddr)
		binary.LittleEndian.PutUint64(buf[off+24:], paddr)
		binary.LittleEndian.PutUint64(buf[off+32:], filesz)
		binary.LittleEndian.PutUint64(buf[off+40:], memsz)
		binary.LittleEndian.PutUint64(buf[off+48:], align)
	}
	putPhdr(offPhdr, 1, 7, 0, 0, 0, uint64(total), uint64(total), 0x1000)                                     // PT_LOAD RWX
	putPhdr(offPhdr+phdrEnt, 2, 6, uint64(offDynamic), uint64(offDynamic), uint64(offDynamic), 6*16, 6*16, 8) // PT_DYNAMIC RW
	putPhdr(offPhdr+2*phdrEnt, 0x6474e551, 6, 0, 0, 0, 0, 0, 0x10)                                            // PT_GNU_STACK (NX)

	// .dynsym: null symbol + the export (GLOBAL FUNC, value = code offset).
	binary.LittleEndian.PutUint32(buf[offDynsym+24:], 1)               // st_name
	buf[offDynsym+28] = 0x12                                           // st_info = GLOBAL|FUNC
	buf[offDynsym+29] = 0                                              // st_other
	binary.LittleEndian.PutUint16(buf[offDynsym+30:], 1)               // st_shndx
	binary.LittleEndian.PutUint64(buf[offDynsym+32:], uint64(offCode)) // st_value
	binary.LittleEndian.PutUint64(buf[offDynsym+40:], 3)               // st_size

	// .dynstr.
	copy(buf[offDynstr:], dynstr)

	// .hash (SysV): 1 bucket, 2 chains, symbol 1 first.
	for i, v := range []uint32{1, 2, 1, 0, 0} {
		binary.LittleEndian.PutUint32(buf[offHash+4*i:], v)
	}

	// The no-op function: xor eax, eax ; ret
	copy(buf[offCode:offCode+3], []byte{0x31, 0xc0, 0xc3})

	// .dynamic: DT_HASH, DT_STRTAB, DT_SYMTAB, DT_STRSZ, DT_SYMENT, DT_NULL.
	dyn := []struct{ tag, val uint64 }{
		{4, uint64(offHash)},
		{5, uint64(offDynstr)},
		{6, uint64(offDynsym)},
		{10, uint64(len(dynstr))},
		{11, 24},
		{0, 0},
	}
	for i, e := range dyn {
		binary.LittleEndian.PutUint64(buf[offDynamic+16*i:], e.tag)
		binary.LittleEndian.PutUint64(buf[offDynamic+16*i+8:], e.val)
	}
	return buf
}

// ensureThreadLoggerShim returns the path of a loadable threadlogger
// shim, generating the shared object if necessary. It prefers a shim
// already installed beside the component (or one written earlier), then
// ~/.screenai, then a temporary file.
func ensureThreadLoggerShim(componentDir string) (string, error) {
	if runtime.GOARCH != "amd64" {
		// The hand-assembled code bytes are x86-64; the component is
		// x64-only anyway, so the load will fail with a clear error.
		return "", errors.New("cannot generate the threadlogger shim on non-amd64")
	}
	// A real compiled shim placed beside the component (or one written
	// earlier) wins over regenerating.
	if componentDir != "" {
		for _, d := range []string{componentDir, filepath.Dir(componentDir)} {
			path := filepath.Join(d, "libthreadlogger_shim.so")
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				return path, nil
			}
		}
	}
	blob := buildThreadLoggerShim()
	candidates := shimCandidateDirs()
	for _, d := range candidates {
		path := filepath.Join(d, "libthreadlogger_shim.so")
		if data, err := os.ReadFile(path); err == nil && bytes.Equal(data, blob) {
			return path, nil
		}
	}
	for _, d := range candidates {
		if err := os.MkdirAll(d, 0o755); err != nil {
			continue
		}
		path := filepath.Join(d, "libthreadlogger_shim.so")
		if err := os.WriteFile(path, blob, 0o644); err == nil {
			return path, nil
		}
	}
	// Last resort: a temp file (stays valid while dlopen has it mapped).
	f, err := os.CreateTemp("", "threadlogger-*.so")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(blob); err != nil {
		f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

func shimCandidateDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".screenai"))
	}
	if cache, err := os.UserCacheDir(); err == nil && cache != "" {
		dirs = append(dirs, filepath.Join(cache, "screenai"))
	}
	return dirs
}
