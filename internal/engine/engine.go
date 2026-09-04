// Package engine loads Google's closed-source Screen AI component
// (libchromescreenai.so / chrome_screen_ai.dll, component id
// mfhmdacoffpmifoibamicehhklffanao — the very binary Chrome itself
// downloads through its component updater) and binds its public C API
// exactly as Chromium's own wrapper does
// (services/screen_ai/screen_ai_library_wrapper_impl.{h,cc}):
//
//	GetLibraryVersion, EnableDebugMode, SetFileContentFunctions,
//	InitOCRUsingCallback, GetMaxImageDimension, SetOCRLightMode,
//	PerformOCR, FreeLibraryAllocatedCharArray,
//	InitMainContentExtractionUsingCallback, ExtractMainContent,
//	FreeLibraryAllocatedInt32Array
//
// Implementation notes recovered while building the original Python
// bridge (all of them load-bearing — do not "simplify" without
// re-testing against the binary):
//
//   - The library asks for its ~64 model files through two callbacks
//     registered with SetFileContentFunctions: a size callback and a
//     content callback that copies the file into a caller-provided
//     buffer.
//   - PerformOCR takes a 48-byte SkBitmap by reference; the layout
//     matches Skia's public struct on x64 (see skbitmap.go).
//   - The Linux library references threadlogger::EnableThreadedLogging,
//     normally provided by Chrome's browser process; a no-op shim is
//     dlopened with RTLD_GLOBAL beforehand (see shim_linux.go). Windows
//     does not need it.
//   - Results are allocated by the library and MUST be released with its
//     own free functions to avoid cross-boundary heap issues.
//   - GetMaxImageDimension() returns 0 until InitOCRUsingCallback() has
//     run — read it only after OCR init, and treat 0 as "no limit".
//   - The engine instance is not thread-safe; every C call runs under
//     the engine lock. Callers that must keep a setting stable across a
//     whole pipeline (switching light mode + OCR + MCE, for example) use
//     Session, which holds the lock for the whole sequence.
package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// ComponentID is Google's component id for "ScreenAI Library"
// (chrome/browser/component_updater/screen_ai_component_installer.cc).
const ComponentID = "mfhmdacoffpmifoibamicehhklffanao"

// LibraryName is the platform-specific file name of the closed-source
// Screen AI binary.
func LibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "chrome_screen_ai.dll"
	case "darwin":
		return "libchromescreenai.dylib"
	default:
		return "libchromescreenai.so"
	}
}

// componentDirNames are the directory names searched (next to the
// executable / in the working directory) for the component, in order.
var componentDirNames = []string{"component", "component_windows", "component_linux", "component_mac"}

// Errors returned deliberately by this package.
var (
	// ErrComponentNotFound reports a missing component directory.
	ErrComponentNotFound = errors.New("screenai: component not found — run 'screenai download' first or pass --component DIR")
	// ErrOCRInit reports a failed InitOCRUsingCallback.
	ErrOCRInit = errors.New("screenai: InitOCRUsingCallback() failed")
	// ErrMCEInit reports a failed InitMainContentExtractionUsingCallback.
	ErrMCEInit = errors.New("screenai: InitMainContentExtractionUsingCallback() failed")
)

// libraryFuncs holds the typed bindings of the library's C API. Each
// platform loader fills it in; pointer results are bound as real Go
// pointers so the values stay GC- and vet-clean end to end.
type libraryFuncs struct {
	getLibraryVersion                      func(major, minor *uint32)
	enableDebugMode                        func()
	setFileContentFunctions                func(sizeCB, contentCB uintptr)
	initOCRUsingCallback                   func() bool
	getMaxImageDimension                   func() uint32
	setOCRLightMode                        func(enabled bool)
	performOCR                             func(bitmap *skBitmap, length *uint32) *byte
	freeLibraryAllocatedCharArray          func(ptr *byte)
	initMainContentExtractionUsingCallback func() bool
	extractMainContent                     func(data *byte, size uint32, count *uint32) *int32
	freeLibraryAllocatedInt32Array         func(ptr *int32)
}

// library is one loaded shared library plus its C callback pointers.
type library struct {
	close      func() error
	funcs      libraryFuncs
	sizeCB     uintptr
	contentCB  uintptr
	loadedFrom string
}

// Options configures an Engine.
type Options struct {
	// ComponentDir overrides auto-detection of the directory holding
	// the library binary and its model files.
	ComponentDir string
	// DebugMode enables the library's debug mode (it dumps I/O protos
	// to the system temp directory).
	DebugMode bool
}

// Engine is one loaded Screen AI library. The OCR and MCE pipelines
// initialize lazily; all C calls are serialized by an internal lock.
type Engine struct {
	componentDir string

	lib   *library
	files *modelFileCache

	mu sync.Mutex

	versionMajor, versionMinor uint32
	versionOnce                bool

	ocrReady bool
	mceReady bool
	ocrLight bool
}

// Locate finds the directory containing the Screen AI binary + models.
// With explicit non-empty, it is used directly; otherwise the usual
// component directory names are searched next to the executable and in
// the working directory.
func Locate(explicit string) (string, error) {
	lib := LibraryName()
	var candidates []string
	if explicit != "" {
		candidates = []string{explicit}
	} else {
		bases := []string{""}
		if exe, err := os.Executable(); err == nil {
			bases = append(bases, filepath.Dir(exe))
		}
		if wd, err := os.Getwd(); err == nil {
			bases = append(bases, wd)
		}
		for _, base := range bases {
			for _, sub := range componentDirNames {
				candidates = append(candidates, filepath.Join(base, sub))
			}
			candidates = append(candidates, base)
		}
	}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, lib)); err == nil && !info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%w (looked for %s in: %s)", ErrComponentNotFound, lib, candidates)
}

// New loads the library and registers the model-file callbacks.
func New(opts Options) (*Engine, error) {
	if err := checkSkBitmapSize(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("screenai: %s is not a supported platform (the component exists for linux, windows and mac x64)", runtime.GOOS)
	}
	dir, err := Locate(opts.ComponentDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		componentDir: dir,
		files:        newModelFileCache(dir),
	}
	lib, err := loadLibrary(filepath.Join(dir, LibraryName()), e.files.sizeCB, e.files.contentCB)
	if err != nil {
		return nil, err
	}
	e.lib = lib
	lib.funcs.setFileContentFunctions(lib.sizeCB, lib.contentCB)
	if opts.DebugMode {
		lib.funcs.enableDebugMode()
	}
	return e, nil
}

// Close releases the library handle.
func (e *Engine) Close() error {
	if e.lib != nil && e.lib.close != nil {
		return e.lib.close()
	}
	return nil
}

// ComponentDir returns the directory the component was loaded from.
func (e *Engine) ComponentDir() string { return e.componentDir }

// Info is a JSON-friendly summary of the engine state.
type Info struct {
	ComponentDir      string `json:"component_dir"`
	Version           string `json:"version"`
	MaxImageDimension uint32 `json:"max_image_dimension"`
	OCRLightMode      bool   `json:"ocr_light_mode"`
	OCRInitialized    bool   `json:"ocr_initialized"`
	MCEInitialized    bool   `json:"mce_initialized"`
}

// Version returns the library version as (major, minor), read once.
func (e *Engine) Version() (uint32, uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.versionLocked()
}

func (e *Engine) versionLocked() (uint32, uint32) {
	if !e.versionOnce {
		e.lib.funcs.getLibraryVersion(&e.versionMajor, &e.versionMinor)
		e.versionOnce = true
	}
	return e.versionMajor, e.versionMinor
}

// VersionString returns the library version formatted "major.minor".
func (e *Engine) VersionString() string {
	major, minor := e.Version()
	return fmt.Sprintf("%d.%d", major, minor)
}

// MaxImageDimension returns the library-side image cap; it is 0 until
// the OCR model is initialized (treat 0 as no limit).
func (e *Engine) MaxImageDimension() uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lib.funcs.getMaxImageDimension()
}

// OCRLightMode reports whether the light OCR model is selected.
func (e *Engine) OCRLightMode() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ocrLight
}

// SetOCRLightMode switches between the full and the light OCR model.
// Switching models un-initializes the OCR pipeline, exactly as in
// Chromium's wrapper.
func (e *Engine) SetOCRLightMode(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setOCRLightModeLocked(enabled)
}

func (e *Engine) setOCRLightModeLocked(enabled bool) {
	e.lib.funcs.setOCRLightMode(enabled)
	e.ocrLight = enabled
	e.ocrReady = false
}

// Info collects the engine state. Reading MaxImageDimension here does
// not initialize anything.
func (e *Engine) Info() Info {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.infoLocked()
}

func (e *Engine) infoLocked() Info {
	return Info{
		ComponentDir:      e.componentDir,
		Version:           e.versionStringLocked(),
		MaxImageDimension: e.lib.funcs.getMaxImageDimension(),
		OCRLightMode:      e.ocrLight,
		OCRInitialized:    e.ocrReady,
		MCEInitialized:    e.mceReady,
	}
}

func (e *Engine) versionStringLocked() string {
	major, minor := e.versionLocked()
	return fmt.Sprintf("%d.%d", major, minor)
}

// InitOCR loads the OCR model (idempotent).
func (e *Engine) InitOCR() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initOCRLocked()
}

func (e *Engine) initOCRLocked() error {
	if !e.ocrReady {
		if !e.lib.funcs.initOCRUsingCallback() {
			return ErrOCRInit
		}
		e.ocrReady = true
	}
	return nil
}

// InitMCE loads the Screen2x model (idempotent).
func (e *Engine) InitMCE() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initMCELocked()
}

func (e *Engine) initMCELocked() error {
	if !e.mceReady {
		if !e.lib.funcs.initMainContentExtractionUsingCallback() {
			return ErrMCEInit
		}
		e.mceReady = true
	}
	return nil
}

// PerformOCR runs the real OCR on a tightly packed RGBA image and
// returns the decoded annotation.
func (e *Engine) PerformOCR(width, height int, rgba []byte) (*pb.Annotation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.performOCRLocked(width, height, rgba)
}

func (e *Engine) performOCRLocked(width, height int, rgba []byte) (*pb.Annotation, error) {
	if err := e.initOCRLocked(); err != nil {
		return nil, err
	}
	if len(rgba) != width*height*4 {
		return nil, fmt.Errorf("screenai: rgba buffer size mismatch (%d bytes for %dx%d)", len(rgba), width, height)
	}
	var pixels *byte
	if len(rgba) > 0 {
		pixels = &rgba[0]
	}
	bitmap := newSkBitmap(pixels, width, height, width*4)
	var length uint32
	ptr := e.lib.funcs.performOCR(&bitmap, &length)
	// The pixel buffer is referenced through a uintptr inside the
	// SkBitmap, invisible to the GC — keep rgba alive until the C call
	// has returned.
	runtime.KeepAlive(rgba)
	if ptr == nil {
		return &pb.Annotation{Lines: []pb.Line{}}, nil
	}
	defer e.lib.funcs.freeLibraryAllocatedCharArray(ptr)
	return pb.DecodeAnnotation(unsafe.Slice(ptr, length))
}

// ExtractMainContent runs the real Screen2x model on a serialized
// screenai.ViewHierarchy and returns the selected axnode ids.
func (e *Engine) ExtractMainContent(viewHierarchy []byte) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.extractMainContentLocked(viewHierarchy)
}

func (e *Engine) extractMainContentLocked(viewHierarchy []byte) ([]int, error) {
	if err := e.initMCELocked(); err != nil {
		return nil, err
	}
	var data *byte
	if len(viewHierarchy) > 0 {
		data = &viewHierarchy[0]
	}
	var count uint32
	ptr := e.lib.funcs.extractMainContent(data, uint32(len(viewHierarchy)), &count)
	runtime.KeepAlive(viewHierarchy)
	if ptr == nil {
		return []int{}, nil
	}
	defer e.lib.funcs.freeLibraryAllocatedInt32Array(ptr)
	arr := unsafe.Slice(ptr, count)
	ids := make([]int, count)
	for i, v := range arr {
		ids[i] = int(v)
	}
	return ids, nil
}

// Session runs fn with the engine lock held for the whole sequence.
// Use it for pipelines that must not interleave with other callers —
// for example switching OCR light mode and then running OCR + MCE. The
// methods of the Session do not re-lock, so they can be combined freely.
func (e *Engine) Session(fn func(*Session) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fn(&Session{e: e})
}

// Session gives fn-style access to the engine while its lock is held.
type Session struct{ e *Engine }

// InitOCR loads the OCR model (idempotent, no re-lock).
func (s *Session) InitOCR() error { return s.e.initOCRLocked() }

// InitMCE loads the Screen2x model (idempotent, no re-lock).
func (s *Session) InitMCE() error { return s.e.initMCELocked() }

// SetOCRLightMode switches the OCR model variant (no re-lock).
func (s *Session) SetOCRLightMode(enabled bool) { s.e.setOCRLightModeLocked(enabled) }

// OCRLightMode reports whether the light OCR model is selected (no
// re-lock).
func (s *Session) OCRLightMode() bool { return s.e.ocrLight }

// PerformOCR runs the real OCR (no re-lock).
func (s *Session) PerformOCR(width, height int, rgba []byte) (*pb.Annotation, error) {
	return s.e.performOCRLocked(width, height, rgba)
}

// ExtractMainContent runs the real Screen2x model (no re-lock).
func (s *Session) ExtractMainContent(viewHierarchy []byte) ([]int, error) {
	return s.e.extractMainContentLocked(viewHierarchy)
}

// Info collects the engine state (no re-lock).
func (s *Session) Info() Info { return s.e.infoLocked() }

// VersionString returns "major.minor" (no re-lock).
func (s *Session) VersionString() string { return s.e.versionStringLocked() }

// MaxImageDimension returns the library-side image cap (no re-lock).
func (s *Session) MaxImageDimension() uint32 { return s.e.lib.funcs.getMaxImageDimension() }
