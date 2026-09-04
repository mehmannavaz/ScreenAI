// Package server exposes the engine over HTTP.
//
// Endpoints (all JSON unless noted):
//
//	GET  /                     tiny human-readable status page
//	GET  /api/info             engine info (version, max dim, light mode)
//	GET  /api/settings         every available setting + effective overrides
//	POST /api/process          the whole pipeline; body = raw file bytes
//	                          query: name=<filename>   (format hint)
//	                                 light=0|1         (OCR light model)
//	                                 maxdim=<px>       (pre-OCR downscale)
//	                                 mce=0|1           (run Screen2x)
//	                                 langs=en,ja       (language filter)
//	                                 min_conf=0.8      (confidence filter)
//	                                 reader=markdown|text|html (reader-view export
//	                                 in the JSON response, field reader_view)
//	                                 crop=0|1          (main_png_base64 crop of
//	                                 the main-content region)
//	GET  /api/sample           one bundled example image
//
// The engine is single-threaded by nature; the whole /api/process
// pipeline runs inside engine.Session so settings cannot interleave
// between concurrent requests.
//
// Engine-level settings (thresholds, script registry, graph params)
// are fixed at startup — the library caches its configs after
// InitOCR — and are configured with --set / --settings on the serve
// command. The language / confidence post-filters are per-request.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mehmannavaz/ScreenAI/examples"
	"github.com/mehmannavaz/ScreenAI/internal/engine"
	"github.com/mehmannavaz/ScreenAI/internal/pipeline"
	"github.com/mehmannavaz/ScreenAI/internal/settings"
)

// DefaultPort is the default TCP port.
const DefaultPort = 8778

// MaxBodyBytes is the upload cap of the HTTP API.
const MaxBodyBytes = 48 << 20

type state struct {
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	mu       sync.Mutex
	last     map[string]any
}

func (s *state) snapshot() map[string]any {
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	return map[string]any{
		"started":  s.started,
		"requests": s.requests.Load(),
		"errors":   s.errors.Load(),
		"last":     last,
	}
}

// Serve creates the engine, prints the endpoints, and serves the API
// until Ctrl+C. s carries the engine-lifetime settings (--set on the
// serve command); the per-request post-filters arrive as query params.
func Serve(bind string, port int, componentDir string, s *settings.Settings) error {
	if s == nil {
		s = settings.Default()
	}
	eng, err := engine.New(engine.Options{ComponentDir: componentDir, Settings: s})
	if err != nil {
		return err
	}
	st := &state{started: time.Now()}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { statusPage(w, r, eng, st) })
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) { apiInfo(w, r, eng, st) })
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) { apiSettings(w, r, s) })
	mux.HandleFunc("/api/process", func(w http.ResponseWriter, r *http.Request) { apiProcess(w, r, eng, st) })
	mux.HandleFunc("/api/sample", apiSample)

	handler := withCORS(logRequests(mux))

	url := fmt.Sprintf("http://%s:%d", displayHost(bind), port)
	fmt.Printf("Chrome Screen AI server — libchromescreenai v%s\n", eng.VersionString())
	fmt.Printf("  component : %s\n", eng.ComponentDir())
	fmt.Printf("  listening : %s  (bind %s)\n", url, bind)
	fmt.Printf("  endpoints : GET /api/info · GET /api/settings · POST /api/process?name=file.png · GET /api/sample\n")
	if eff := s.Effective(); len(eff) > 0 {
		fmt.Printf("  settings  : %s\n", strings.Join(eff, " · "))
	}
	fmt.Printf("  Press Ctrl+C to stop.\n")

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", bind, port),
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	return srv.ListenAndServe()
}

func displayHost(bind string) string {
	if bind == "0.0.0.0" || bind == "" {
		return "127.0.0.1"
	}
	return bind
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.RemoteAddr, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, doc any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(doc)
}

func apiInfo(w http.ResponseWriter, r *http.Request, eng *engine.Engine, st *state) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET only"})
		return
	}
	var info engine.Info
	err := eng.Session(func(s *engine.Session) error {
		// Initialize the OCR model so max_image_dimension reports the
		// real limit (it is 0 until InitOCRUsingCallback has run) and
		// the /api/info call verifies the engine end-to-end.
		if err := s.InitOCR(); err != nil {
			return err
		}
		info = s.Info()
		return nil
	})
	if err != nil {
		st.errors.Add(1)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	snap := st.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"library":             "libchromescreenai",
		"version":             info.Version,
		"max_image_dimension": info.MaxImageDimension,
		"ocr_light_mode":      info.OCRLightMode,
		"component_dir":       info.ComponentDir,
		"uptime_s":            int(time.Since(st.started).Seconds()),
		"requests":            snap["requests"],
		"errors":              snap["errors"],
	})
}

func apiProcess(w http.ResponseWriter, r *http.Request, eng *engine.Engine, st *state) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST only"})
		return
	}
	q := r.URL.Query()
	name := q.Get("name")
	if name == "" {
		name = "upload.bin"
	}
	light := q.Get("light") == "1" || q.Get("light") == "true"
	maxdim := 0
	fmt.Sscanf(q.Get("maxdim"), "%d", &maxdim)
	wantMCE := !(q.Get("mce") == "0" || q.Get("mce") == "false")
	var langs []string
	if v := q.Get("langs"); v != "" {
		langs = strings.Split(v, ",")
	}
	minConf := 0.0
	fmt.Sscanf(q.Get("min_conf"), "%g", &minConf)
	readerFmt := ""
	if v := q.Get("reader"); v != "" {
		f, err := settings.NormalizeReaderFormat(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"ok": false, "error": fmt.Sprintf("reader: %v", err)})
			return
		}
		if f == "" || f == "none" {
			f = "markdown" // bare reader= means markdown
		}
		readerFmt = f
	}
	cropMain := q.Get("crop") == "1" || q.Get("crop") == "true"

	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	data, err := readAll(r)
	if err != nil {
		st.errors.Add(1)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"ok": false, "error": fmt.Sprintf("body too large (limit %d bytes)", MaxBodyBytes)})
		return
	}
	if len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "empty body — POST the raw file bytes with ?name=<filename>"})
		return
	}

	st.requests.Add(1)
	t0 := time.Now()
	inp, err := pipeline.Classify(data, name, "", name)
	if err == nil {
		// The whole pipeline runs under the engine lock (Session) so a
		// light-mode switch cannot interleave with another request.
		var res *pipeline.Result
		err = eng.Session(func(s *engine.Session) error {
			var perr error
			res, _, perr = pipeline.Process(s, inp, pipeline.Options{
				Light: light, MaxDim: maxdim, MCE: wantMCE,
				Languages: langs, MinConfidence: minConf,
				ReaderFormat: readerFmt, CropMain: cropMain,
				Effective: eng.Settings().Effective(),
			})
			return perr
		})
		if err == nil {
			st.note(name, int(time.Since(t0).Milliseconds()))
			writeJSON(w, http.StatusOK, res)
			return
		}
	}
	st.errors.Add(1)
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error(), "name": name})
}

func readAll(r *http.Request) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, err
			}
			return out, nil
		}
	}
}

func (s *state) note(name string, ms int) {
	s.mu.Lock()
	s.last = map[string]any{"name": name, "ms": ms}
	s.mu.Unlock()
}

func apiSettings(w http.ResponseWriter, r *http.Request, s *settings.Settings) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET only"})
		return
	}
	docs := make([]map[string]any, 0, len(settings.Descriptors))
	for _, d := range settings.Descriptors {
		docs = append(docs, map[string]any{
			"key":       d.Key,
			"type":      d.Type,
			"default":   d.Default,
			"mechanism": string(d.Mechanism),
			"summary":   d.Summary,
			"effect":    d.Effect,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"settings":  docs,
		"effective": s.Effective(),
		"note":      "Engine-level settings are fixed while the server runs (the library caches configs after InitOCR); restart with different --set values to change them. langs and min_conf are per-request query params on /api/process.",
	})
}

func apiSample(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET only"})
		return
	}
	png := examples.SampleNewsPNG()
	if png == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no sample available"})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="sample_news.png"`)
	w.Header().Set("Content-Length", fmt.Sprint(len(png)))
	w.WriteHeader(http.StatusOK)
	w.Write(png)
}

func statusPage(w http.ResponseWriter, r *http.Request, eng *engine.Engine, st *state) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/api" {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "not found",
			"routes": []string{"/api/info", "/api/process", "/api/sample"}})
		return
	}
	version := eng.VersionString()
	comp := eng.ComponentDir()
	snap := st.snapshot()
	uptime := int(time.Since(st.started).Seconds())
	html := `<!doctype html><html><head><meta charset="utf-8">
<title>Screen AI server</title><style>
body{font-family:system-ui,sans-serif;background:#101418;color:#e8eaed;
margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
div{max-width:560px;padding:32px}h1{font-size:22px;margin:0 0 4px}
p{color:#9aa0a6;margin:4px 0}code{background:#1f242c;padding:2px 6px;
border-radius:4px;color:#8ab4f8}</style></head><body><div>
<h1>Chrome Screen AI server</h1>
<p>engine <code>libchromescreenai v` + version + `</code></p>
<p>component: <code>` + htmlEscape(comp) + `</code></p>
<p>up ` + fmt.Sprint(uptime) + `s &middot; ` + fmt.Sprint(snap["requests"]) + ` requests &middot; ` + fmt.Sprint(snap["errors"]) + ` errors</p>
<p>Use the HTTP API:</p>
<p><code>curl -s --data-binary @photo.png 'http://HOST:` + fmt.Sprint(DefaultPort) + `/api/process?name=photo.png' | python -m json.tool | head</code></p>
</div></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, html)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}
