// Package componentdl downloads Google's Screen AI component.
//
// The Screen AI library is closed-source and NOT in the Chromium git
// repo — Google ships it as a prebuilt binary through Chrome's component
// updater (Omaha protocol). This package downloads it from the exact
// same public endpoint Chrome uses, then extracts the platform library
// and its model files.
//
// The ~124 MB transfer is partitioned into N parts fetched in parallel
// with HTTP Range requests. Every part is resumable: if the connection
// drops, re-run the same command — already-downloaded bytes are kept and
// only the missing ranges are fetched.
package componentdl

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ProdVersion is the Chrome version the component endpoint is gated on.
const ProdVersion = "120.0.0.0"

// ErrDownload reports a failed component download.
var ErrDownload = errors.New("download failed")

// Options configure a component download.
type Options struct {
	// Target is the output directory (default "component").
	Target string
	// OSName is "win", "linux" or "mac" (default: this machine).
	OSName string
	// Arch is "x64" or "arm64" (default x64 — the only build Google
	// actually ships for linux).
	Arch string
	// Parts is the number of parallel ranged partitions.
	Parts int
	// Single forces a single-stream download.
	Single bool
	// Quiet suppresses the progress lines.
	Quiet bool
}

// CRXURL returns the exact URL Chrome's updater redirects through.
func CRXURL(osName, arch string) string {
	return "https://clients2.google.com/service/update2/crx?response=redirect" +
		"&acceptformat=crx2,crx3&prodversion=" + ProdVersion +
		"&os=" + osName + "&arch=" + arch +
		"&x=id%3Dmfhmdacoffpmifoibamicehhklffanao%26installsource%3Dondemand%26uc"
}

// LibraryNameFor returns the component's library file name.
func LibraryNameFor(osName string) string {
	switch osName {
	case "win":
		return "chrome_screen_ai.dll"
	case "mac":
		return "libchromescreenai.dylib"
	default:
		return "libchromescreenai.so"
	}
}

func defaultOS() string {
	switch runtime.GOOS {
	case "windows":
		return "win"
	case "darwin":
		return "mac"
	default:
		return "linux"
	}
}

type client struct {
	http *http.Client
}

func newClient() *client {
	return &client{http: &http.Client{Timeout: 0}}
}

func (c *client) get(url string, extraHeaders map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

// Download fetches + extracts the component and returns the library
// path. It skips the download when the library is already present.
func Download(opts Options) (string, error) {
	if opts.OSName == "" {
		opts.OSName = defaultOS()
	}
	if opts.Arch == "" {
		opts.Arch = "x64"
	}
	if opts.Target == "" {
		opts.Target = "component"
	}
	if opts.Parts <= 0 {
		opts.Parts = 10
	}

	target, err := filepath.Abs(opts.Target)
	if err != nil {
		target = opts.Target
	}
	lib := LibraryNameFor(opts.OSName)
	libPath := filepath.Join(target, lib)
	if info, err := os.Stat(libPath); err == nil && !info.IsDir() {
		fmt.Printf("component already present: %s\n", libPath)
		return libPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDownload, err)
	}
	crx := target + ".crx"
	url := CRXURL(opts.OSName, opts.Arch)
	parts := opts.Parts
	if opts.Single {
		parts = 1
	}
	c := newClient()
	if err := c.downloadFile(url, crx, parts, opts.Quiet); err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDownload, err)
	}
	if _, err := ExtractCRX(crx, target); err != nil {
		return "", err
	}
	if err := os.Remove(crx); err != nil {
		// keeping the .crx around is harmless
		_ = err
	}
	if _, err := os.Stat(libPath); err != nil {
		return "", fmt.Errorf("%w: expected %s after extraction — check %s", ErrDownload, lib, target)
	}
	fmt.Printf("OK: %s — the real closed-source Screen AI binary is ready\n", libPath)
	return libPath, nil
}

// downloadFile partitions the transfer when the server supports ranged
// requests, otherwise falls back to a single stream.
func (c *client) downloadFile(url, dest string, parts int, quiet bool) error {
	total, supportsRange := c.probeSize(url)
	if !supportsRange || parts <= 1 || total < 8<<20 {
		return c.downloadSingle(url, dest, quiet)
	}

	fmt.Printf("downloading %s\n", url)
	fmt.Printf("  %.1f MB in %d parts (~%.1f MB each, parallel + resumable)\n",
		float64(total)/1e6, parts, float64(total)/1e6/float64(parts))

	chunk := total / parts
	bounds := make([][2]int, parts)
	for i := 0; i < parts; i++ {
		a := i * chunk
		b := total - 1
		if i != parts-1 {
			b = (i+1)*chunk - 1
		}
		bounds[i] = [2]int{a, b}
	}
	names := make([]string, parts)
	for i := range names {
		names[i] = fmt.Sprintf("%s.part%02d", dest, i+1)
	}

	progress := make([]int, parts)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, parts)
	for i := 0; i < parts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.fetchRange(url, bounds[i][0], bounds[i][1], names[i], &progress[i], &mu)
		}(i)
	}

	// Progress ticker.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				mu.Lock()
				sum := 0
				for _, p := range progress {
					sum += p
				}
				mu.Unlock()
				if !quiet {
					fmt.Printf("\r  %7.1f / %.1f MB (%5.1f%%)", float64(sum)/1e6, float64(total)/1e6, 100*float64(sum)/float64(total))
				}
			}
		}
	}()
	wg.Wait()
	close(done)
	fmt.Println()

	for i, err := range errs {
		if err != nil {
			fmt.Printf("  part %02d failed: %v\n", i+1, err)
			return fmt.Errorf("%w: incomplete — re-run to resume only the missing bytes", ErrDownload)
		}
	}

	// Assemble.
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer out.Close()
	for i, name := range names {
		want := bounds[i][1] - bounds[i][0] + 1
		info, err := os.Stat(name)
		if err != nil || info.Size() != int64(want) {
			return fmt.Errorf("%w: %s: size mismatch, delete and re-run", ErrDownload, name)
		}
		if err := copyFileInto(out, name); err != nil {
			return fmt.Errorf("%w: %v", ErrDownload, err)
		}
	}
	if info, err := os.Stat(dest); err != nil || info.Size() != int64(total) {
		return fmt.Errorf("%w: assembled file has wrong size — delete parts and re-run", ErrDownload)
	}
	for _, name := range names {
		os.Remove(name)
	}
	fmt.Printf("  saved %d bytes (%d parts joined)\n", total, parts)
	return nil
}

func (c *client) downloadSingle(url, dest string, quiet bool) error {
	fmt.Printf("downloading (single stream) %s\n", url)
	resp, err := c.get(url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrDownload, resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer out.Close()
	var total int64
	buf := make([]byte, 1<<20)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fmt.Errorf("%w: %v", ErrDownload, werr)
			}
			total += int64(n)
			if !quiet {
				fmt.Printf("\r  %7.1f MB", float64(total)/1e6)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDownload, err)
		}
	}
	fmt.Printf("\n  saved %d bytes\n", total)
	return nil
}

// probeSize returns the total size when the server supports Range.
func (c *client) probeSize(url string) (int, bool) {
	resp, err := c.get(url, map[string]string{"Range": "bytes=0-0"})
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, false
	}
	cr := resp.Header.Get("Content-Range")
	// "bytes 0-0/12345"
	if !strings.HasPrefix(cr, "bytes 0-0/") {
		return 0, false
	}
	var total int
	if _, err := fmt.Sscanf(cr[len("bytes 0-0/"):], "%d", &total); err != nil {
		return 0, false
	}
	return total, true
}

// fetchRange downloads [start, end] to outPath, resuming partial data,
// with bounded retries and exponential backoff.
func (c *client) fetchRange(url string, start, end int, outPath string, progress *int, mu *sync.Mutex) error {
	expected := end - start + 1
	const retries = 12
	for attempt := 0; ; attempt++ {
		have := fileSize(outPath)
		mu.Lock()
		*progress = have
		mu.Unlock()
		if have >= expected {
			return nil
		}
		err := c.fetchRangeOnce(url, start+have, end, outPath, progress, mu)
		if err == nil && fileSize(outPath) >= expected {
			return nil
		}
		if err == nil {
			err = errors.New("short read")
		}
		if attempt >= retries-1 {
			return err
		}
		time.Sleep(time.Duration(min(2*(attempt+1), 15)) * time.Second)
	}
}

func (c *client) fetchRangeOnce(url string, from, end int, outPath string, progress *int, mu *sync.Mutex) error {
	resp, err := c.get(url, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", from, end)})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("%w: server ignored Range (HTTP %d); retry with --single", ErrDownload, resp.StatusCode)
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<18)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			mu.Lock()
			*progress += n
			mu.Unlock()
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func fileSize(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(info.Size())
}

func copyFileInto(out io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return err
}

// ExtractCRX unpacks a CRX3 container: magic(4) + version(4) +
// header_len(4) + header + ZIP. It returns the number of extracted
// files.
func ExtractCRX(crxPath, outDir string) (int, error) {
	f, err := os.Open(crxPath)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	defer f.Close()
	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, fmt.Errorf("%w: %s: not a CRX file", ErrDownload, crxPath)
	}
	if string(header[:4]) != "Cr24" {
		return 0, fmt.Errorf("%w: %s: not a CRX file (bad magic)", ErrDownload, crxPath)
	}
	version := binary.LittleEndian.Uint32(header[4:8])
	headerLen := binary.LittleEndian.Uint32(header[8:12])
	zipOffset := int64(12) + int64(headerLen)
	zipSize := mustSize(crxPath) - zipOffset
	if zipSize <= 0 {
		return 0, fmt.Errorf("%w: %s: CRX header larger than file", ErrDownload, crxPath)
	}
	fmt.Printf("crx v%d, extracting ...\n", version)

	// zip.NewReader works on relative offsets — a SectionReader rooted at
	// the ZIP start provides exactly that view of the tail of the CRX.
	zr, err := zip.NewReader(io.NewSectionReader(f, zipOffset, zipSize), zipSize)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	count := 0
	for _, zf := range zr.File {
		// zip-slip protection: only extract relative paths that stay
		// inside outDir.
		name := filepath.Clean(zf.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
			continue
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(outDir, name), 0o755); err != nil {
				return count, err
			}
			continue
		}
		if err := extractZipFile(zf, filepath.Join(outDir, name)); err != nil {
			return count, err
		}
		count++
	}
	fmt.Printf("extracted %d files to %s\n", count, outDir)
	return count, nil
}

func extractZipFile(zf *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func mustSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
