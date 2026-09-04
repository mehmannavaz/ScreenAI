//go:build linux

package engine

import (
	"fmt"
	"path/filepath"

	"github.com/ebitengine/purego"
)

// loadLibrary dlopen()s the component and binds its public C API through
// purego, without cgo — so ScreenAI cross-compiles with CGO_ENABLED=0
// and needs no C toolchain on the build machine.
//
// The no-op threadlogger shim is loaded first with RTLD_GLOBAL so the
// component's reference to threadlogger::EnableThreadedLogging resolves.
func loadLibrary(path string, sizeFn func(*byte) uint32, contentFn func(*byte, uint32, *byte)) (*library, error) {
	if shimPath, err := ensureThreadLoggerShim(filepath.Dir(path)); err == nil {
		if _, err := purego.Dlopen(shimPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return nil, fmt.Errorf("loading threadlogger shim %s: %w", shimPath, err)
		}
	}
	// A missing shim is non-fatal: the failure then surfaces as dlopen's
	// own "undefined symbol" message if the library really needs it.

	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("dlopen %s: %w", path, err)
	}

	bind := func(name string, fn any) error {
		addr, err := purego.Dlsym(handle, name)
		if err != nil {
			return fmt.Errorf("resolving %s in %s: %w", name, path, err)
		}
		purego.RegisterFunc(fn, addr)
		return nil
	}

	var f libraryFuncs
	for _, b := range []struct {
		name string
		fn   any
	}{
		{"GetLibraryVersion", &f.getLibraryVersion},
		{"EnableDebugMode", &f.enableDebugMode},
		{"SetFileContentFunctions", &f.setFileContentFunctions},
		{"InitOCRUsingCallback", &f.initOCRUsingCallback},
		{"GetMaxImageDimension", &f.getMaxImageDimension},
		{"SetOCRLightMode", &f.setOCRLightMode},
		{"PerformOCR", &f.performOCR},
		{"FreeLibraryAllocatedCharArray", &f.freeLibraryAllocatedCharArray},
		{"InitMainContentExtractionUsingCallback", &f.initMainContentExtractionUsingCallback},
		{"ExtractMainContent", &f.extractMainContent},
		{"FreeLibraryAllocatedInt32Array", &f.freeLibraryAllocatedInt32Array},
	} {
		if err := bind(b.name, b.fn); err != nil {
			return nil, err
		}
	}

	// The library is deliberately NEVER dlclosed: it spawns background
	// threads (TFLite pools, the fiber scheduler) and its static
	// destructors corrupt any later use — the same reason it cannot be
	// re-initialized in-process (verified: a second InitOCR aborts).
	// Chrome keeps the component loaded for the process lifetime too.
	// purego callback memory is likewise never freed, so the pointers
	// stay valid for the lifetime of the process.
	return &library{
		funcs:      f,
		sizeCB:     purego.NewCallback(sizeFn),
		contentCB:  purego.NewCallback(contentFn),
		loadedFrom: path,
	}, nil
}
