package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/mehmannavaz/ScreenAI/internal/settings"
)

// modelFileCache serves the ~64 model files the library requests through
// the SetFileContentFunctions callbacks. The library asks by relative path
// and expects the bytes to come from the component directory.
//
// When settings overrides are active, the bytes of the four known
// config files are rewritten on first read (see internal/settings) —
// the library then initializes its pipelines from the patched
// configs, which is how thresholds, script restrictions and graph
// parameters reach the closed-source binary.
//
// The cache has its own lock: the callbacks are invoked from inside
// library calls (while the engine lock is held by the caller), so sharing
// the engine lock would deadlock.
type modelFileCache struct {
	dir      string
	settings *settings.Settings
	mu       sync.Mutex
	files    map[string][]byte
}

func newModelFileCache(dir string, s *settings.Settings) *modelFileCache {
	return &modelFileCache{dir: dir, settings: s, files: map[string][]byte{}}
}

// path resolves a library-relative name (any slash style, possibly with a
// leading slash) to a path inside the component directory.
func (c *modelFileCache) path(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimLeft(rel, "/")
	return filepath.Join(append([]string{c.dir}, strings.Split(rel, "/")...)...)
}

// read returns the file contents (empty when unreadable), caching the
// settings-patched bytes. Setting SCREENAI_TRACE_FILES=1 prints every
// library file request to stderr — handy for debugging what the
// pipeline actually loads.
func (c *modelFileCache) read(rel string) []byte {
	if os.Getenv("SCREENAI_TRACE_FILES") != "" {
		fmt.Fprintf(os.Stderr, "VFSREQ: %s\n", rel)
	}
	path := c.path(rel)
	c.mu.Lock()
	defer c.mu.Unlock()
	if data, ok := c.files[path]; ok {
		return data
	}
	data, err := os.ReadFile(path)
	if err != nil {
		data = nil
	}
	if data != nil && c.settings.NonEmpty() {
		patched, perr := c.settings.Apply(rel, data)
		if perr != nil {
			// Fail loudly but keep the engine alive: a broken
			// override degrades to the stock config instead of
			// taking the whole pipeline down.
			fmt.Fprintf(os.Stderr, "screenai: settings override for %s failed: %v (using stock config)\n", rel, perr)
		} else {
			data = patched
		}
	}
	c.files[path] = data
	return data
}

// sizeCB implements uint32_t (*)(const char*): the byte size of a model
// file, 0 when unknown.
//
// The Go result is uintptr, not uint32: purego.NewCallback on Windows
// delegates to syscall.NewCallback, whose runtime check accepts exactly
// one pointer-sized result — a uint32 result panics with
// "compileCallback: expected function with one uintptr-sized result".
// The library reads only the low 32 bits of the return register, so the
// value it sees is unchanged.
func (c *modelFileCache) sizeCB(name *byte) uintptr {
	data := c.read(cString(name))
	if len(data) >= 0xFFFFFFFF {
		return 0xFFFFFFFF
	}
	return uintptr(len(data))
}

// contentCB implements void (*)(const char*, uint32_t, char*): copies the
// file into the library-provided buffer (up to its size).
//
// The Go result is uintptr because purego.NewCallback on Windows
// delegates to syscall.NewCallback, whose runtime check requires exactly
// one pointer-sized result — a void signature panics with
// "compileCallback: expected function with one uintptr-sized result".
// The library declares the callback void, so the returned 0 is never
// read.
func (c *modelFileCache) contentCB(name *byte, bufferSize uint32, buffer *byte) uintptr {
	if buffer == nil || bufferSize == 0 {
		return 0
	}
	data := c.read(cString(name))
	n := len(data)
	if n > int(bufferSize) {
		n = int(bufferSize)
	}
	if n > 0 {
		copy(unsafe.Slice(buffer, int(bufferSize))[:n], data[:n])
	}
	return 0
}

// cString converts a NUL-terminated C string to a Go string (empty for
// nil). The bytes are copied, so the result stays valid after the C
// buffer is reused.
func cString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for q := unsafe.Pointer(p); ; n++ {
		if *(*byte)(q) == 0 {
			break
		}
		q = unsafe.Add(q, 1)
	}
	return string(unsafe.Slice(p, n))
}
