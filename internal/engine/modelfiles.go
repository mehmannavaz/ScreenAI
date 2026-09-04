package engine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

// modelFileCache serves the ~64 model files the library requests through
// the SetFileContentFunctions callbacks. The library asks by relative path
// and expects the bytes to come from the component directory.
//
// The cache has its own lock: the callbacks are invoked from inside
// library calls (while the engine lock is held by the caller), so sharing
// the engine lock would deadlock.
type modelFileCache struct {
	dir   string
	mu    sync.Mutex
	files map[string][]byte
}

func newModelFileCache(dir string) *modelFileCache {
	return &modelFileCache{dir: dir, files: map[string][]byte{}}
}

// path resolves a library-relative name (any slash style, possibly with a
// leading slash) to a path inside the component directory.
func (c *modelFileCache) path(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimLeft(rel, "/")
	return filepath.Join(append([]string{c.dir}, strings.Split(rel, "/")...)...)
}

// read returns the file contents (empty when unreadable), caching results.
func (c *modelFileCache) read(rel string) []byte {
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
	c.files[path] = data
	return data
}

// sizeCB implements uint32_t (*)(const char*): the byte size of a model
// file, 0 when unknown.
func (c *modelFileCache) sizeCB(name *byte) uint32 {
	data := c.read(cString(name))
	if len(data) >= 0xFFFFFFFF {
		return 0xFFFFFFFF
	}
	return uint32(len(data))
}

// contentCB implements void (*)(const char*, uint32_t, char*): copies the
// file into the library-provided buffer (up to its size).
func (c *modelFileCache) contentCB(name *byte, bufferSize uint32, buffer *byte) {
	if buffer == nil || bufferSize == 0 {
		return
	}
	data := c.read(cString(name))
	n := len(data)
	if n > int(bufferSize) {
		n = int(bufferSize)
	}
	if n > 0 {
		copy(unsafe.Slice(buffer, int(bufferSize))[:n], data[:n])
	}
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
