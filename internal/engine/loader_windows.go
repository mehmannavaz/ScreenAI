//go:build windows

package engine

import (
	"fmt"
	"syscall"

	"github.com/ebitengine/purego"
)

// loadLibrary LoadLibrary()s the component DLL and binds its public C API
// through purego (which routes calls through syscall.SyscallN), with the
// handle from the standard library — no cgo, no mingw, so the Windows
// build needs no C toolchain.
//
// The Windows component has no threadlogger dependency, so no shim is
// involved. On x64 there is a single calling convention, so the plain C
// declarations bind directly.
func loadLibrary(path string, sizeFn func(*byte) uint32, contentFn func(*byte, uint32, *byte)) (*library, error) {
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, fmt.Errorf("LoadLibrary %s: %w", path, err)
	}

	bind := func(name string, fn any) error {
		proc, err := dll.FindProc(name)
		if err != nil {
			return fmt.Errorf("resolving %s in %s: %w", name, path, err)
		}
		purego.RegisterFunc(fn, proc.Addr())
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
			dll.Release()
			return nil, err
		}
	}

	return &library{
		close:      dll.Release,
		funcs:      f,
		sizeCB:     purego.NewCallback(sizeFn),
		contentCB:  purego.NewCallback(contentFn),
		loadedFrom: path,
	}, nil
}
