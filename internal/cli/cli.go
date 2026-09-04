// Package cli implements the screenai command line interface.
//
// Commands:
//
//	screenai run FILE...     process inputs (default when bare files are given)
//	screenai demo            run the bundled examples
//	screenai info            show library + component info
//	screenai settings        print the full settings reference
//	screenai init            write a documented settings.json
//	screenai serve           serve the engine as an HTTP API
//	screenai download        fetch the Screen AI component from Google
//	screenai selftest        verify codecs (and the engine when present)
package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mehmannavaz/ScreenAI/examples"
	"github.com/mehmannavaz/ScreenAI/internal/componentdl"
	"github.com/mehmannavaz/ScreenAI/internal/engine"
	"github.com/mehmannavaz/ScreenAI/internal/pipeline"
	"github.com/mehmannavaz/ScreenAI/internal/report"
	"github.com/mehmannavaz/ScreenAI/internal/selftest"
	"github.com/mehmannavaz/ScreenAI/internal/server"
	"github.com/mehmannavaz/ScreenAI/internal/settings"
)

// Version is stamped at build time by -ldflags "-X ...cli.Version=...".
var Version = "dev"

const usage = `screenai — run Google's real closed-source Screen AI (OCR + Screen2x
main content) on any input: images (PNG/JPEG/GIF/BMP/TGA/PPM), HTML pages,
JSON page trees, plain text.

usage:
  screenai [command] [flags] [inputs...]

commands:
  run FILE...     process input files or http(s):// URLs (default command)
  demo            run the bundled examples (reader view enabled)
  info            show library + component info
  settings        print every available setting with defaults + effects
                  --json  machine-readable reference + settings.json template
  init [FILE]     write a fully documented settings.json (default
                  ./settings.json; --force to overwrite)
  serve           serve the engine as an HTTP API (default port 8778)
  download        fetch the Screen AI component from Google (~124 MB)
  selftest        verify codecs (and the engine when present)
  version         print the version

settings.json:
  ./settings.json (or $SCREENAI_SETTINGS) is auto-loaded when present —
  create one with ` + "`screenai init`" + `. It may be flat
  ("mce.threshold_non_essential": 0.99) or nested ("mce": {...}).
  --settings FILE and --set k=v override it, in that order.

flags (run/demo):
  -o DIR          output directory (default ./results)
  --light         use the light OCR model (= --set ocr.light_mode=true)
  --no-mce        skip main-content extraction (= --set mce.enabled=false)
  --no-html       write JSON only, no HTML reports
  --maxdim PX     extra pre-OCR downscale cap (0 = off)
  --langs A,B     keep only OCR lines in these languages (en, ja, ...)
  --min-conf F    drop OCR lines below confidence F (0..1)
  --reader         export the reader view as markdown (see --reader-format)
  --reader-format F  reader export format: text|markdown|html
  --crop-main     crop the main-content region to <stem>.main.png
  --no-ocr-boxes  omit the OCR box overlay from the HTML report
  --no-mce-boxes  omit the main-content box overlay from the HTML report
  --no-boxes      omit both overlays (= --no-ocr-boxes --no-mce-boxes)
  --print         print the reader view to stdout
  --set K=V       override any setting (repeatable; see ` + "`screenai settings`" + `)
  --settings FILE JSON file of settings overrides
  --debug         library debug mode (dumps i/o protos to tmp)

common flags:
  --component DIR Screen AI component directory (auto-detected)
  -v, -vv         more logging

examples:
  screenai download
  screenai info
  screenai run photo.png page.html notes.txt
  screenai run --langs en --min-conf 0.8 scan.png
  screenai run --reader --crop-main article.png
  screenai run --set mce.threshold_non_essential=0.9 photo.png
  screenai run --set ocr.scripts=und,Jpan,Kore,Hani japan.jpg
  screenai init && screenai run page.html
  screenai demo
  screenai serve --port 8778 --set mce.threshold_non_essential=0.85

The engine is Google's genuine ScreenAI component (id
mfhmdacoffpmifoibamicehhklffanao) driven through its public C API.
Every knob the component exposes was extracted by deep analysis; run
` + "`screenai settings`" + ` for the full reference. There is no top-k or
temperature anywhere — the models are deterministic.`

// Main runs the CLI and returns the process exit code.
func Main(argv []string) int {
	// Global options may appear anywhere (--component X info and
	// info --component X are equivalent).
	common, rest := splitCommon(argv)

	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	cmd, args := rest[0], rest[1:]
	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("screenai " + Version)
		return 0
	case "help", "-h", "--help":
		fmt.Println(usage)
		return 0
	case "settings":
		if len(args) > 0 && (args[0] == "--json" || args[0] == "-json") {
			fmt.Println(settings.ReferenceJSON())
		} else {
			fmt.Print(settings.Reference())
		}
		return 0
	case "init":
		return cmdInit(args)
	case "run", "demo", "info", "serve", "download", "selftest":
		// handled below
	default:
		// bare file arguments mean `run`
		cmd, args = "run", rest
	}
	// Flags may appear after positional arguments too (Go's flag package
	// otherwise stops at the first positional).
	args = hoistFlags(args, knownValueFlags)

	switch cmd {
	case "selftest":
		fs := flag.NewFlagSet("selftest", flag.ExitOnError)
		component := fs.String("component", "", "component directory")
		_ = fs.Parse(args)
		dir := *component
		if dir == "" {
			dir = common.component
		}
		results := selftest.Run(dir)
		// The live A/B check runs the binary itself as a
		// subprocess (the closed-source library cannot be
		// re-initialized in-process), so it only works from
		// the real binary — not under `go test`.
		if _, err := engine.Locate(dir); err == nil {
			if exe, err := os.Executable(); err == nil {
				if tmp, err := os.MkdirTemp("", "screenai-ab"); err == nil {
					defer os.RemoveAll(tmp)
					results = append(results, selftest.SettingsLiveAB(exe, dir, tmp))
				}
			}
		}
		if err := selftest.PrintAndReport(results); err != nil {
			return 1
		}
		return 0

	case "download":
		fs := flag.NewFlagSet("download", flag.ExitOnError)
		osName := fs.String("os", "", "win | linux | mac (default: this machine)")
		arch := fs.String("arch", "x64", "x64 | arm64")
		parts := fs.Int("parts", 10, "parallel download partitions")
		single := fs.Bool("single", false, "force a single-stream download")
		target := fs.String("dir", "component", "target directory")
		fs.Parse(args)
		if _, err := componentdl.Download(componentdl.Options{
			Target: *target, OSName: *osName, Arch: *arch,
			Parts: *parts, Single: *single,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 1
		}
		return 0

	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		port := fs.Int("port", server.DefaultPort, "TCP port")
		bind := fs.String("bind", "0.0.0.0", "bind address")
		s, err := parseSettingsFlags(fs, args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 2
		}
		if err := server.Serve(*bind, *port, common.component, s); err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 1
		}
		return 0
	}

	// run / demo / info all need the engine.
	opts, s, err := parsePipeline(cmd, args, common)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 2
	}
	eng, err := engine.New(engine.Options{
		ComponentDir: common.component,
		Settings:     s,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 1
	}
	defer eng.Close()

	if cmd == "info" {
		return cmdInfo(eng)
	}

	sources := opts.sources
	if cmd == "demo" {
		var err error
		sources, err = materializeExamples(opts.outdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 1
		}
	}
	return cmdRun(eng, sources, opts, s)
}

type runOptions struct {
	sources     []string
	outdir      string
	light       bool
	mce         bool
	html        bool
	maxdim      int
	readerFmt   string
	cropMain    bool
	printReader bool
}

type commonOpts struct {
	component string
}

// knownValueFlags are the flags that take a separate value argument.
// (--reader and --crop-main are plain booleans — they must NOT be
// listed here, or `--reader photo.png` would swallow the file name.)
var knownValueFlags = map[string]bool{
	"o": true, "component": true, "maxdim": true,
	"port": true, "bind": true, "parts": true,
	"os": true, "arch": true, "dir": true,
	"langs": true, "min-conf": true, "set": true, "settings": true,
	"reader-format": true,
}

// hoistFlags moves recognized flag tokens (and their values) in front of
// the positional arguments, so both `run -o out a.png` and `run a.png -o
// out` work — Go's flag package otherwise stops at the first positional.
func hoistFlags(args []string, valueFlags map[string]bool) []string {
	var flags, rest []string
	i := 0
	for i < len(args) {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		if strings.HasPrefix(a, "-") && name != "" {
			if eq := strings.Index(name, "="); eq >= 0 {
				name = name[:eq]
				flags = append(flags, a)
				i++
				continue
			}
			if valueFlags[name] && i+1 < len(args) {
				flags = append(flags, a, args[i+1])
				i += 2
				continue
			}
			flags = append(flags, a)
			i++
			continue
		}
		rest = append(rest, a)
		i++
	}
	return append(flags, rest...)
}

// splitCommon extracts --component / -v / -vv from anywhere in argv.
func splitCommon(argv []string) (commonOpts, []string) {
	var common commonOpts
	var rest []string
	i := 0
	for i < len(argv) {
		a := argv[i]
		switch {
		case a == "--component" && i+1 < len(argv):
			common.component = argv[i+1]
			i += 2
		case strings.HasPrefix(a, "--component="):
			common.component = strings.SplitN(a, "=", 2)[1]
			i++
		case a == "-v" || a == "-vv" || a == "--verbose":
			i++ // accepted for symmetry; logging is stderr-quiet by design
		default:
			rest = append(rest, a)
			i++
		}
	}
	return common, rest
}

// addSettingsFlags registers the shared settings flags on fs and
// returns a function that folds everything into a *Settings together
// with the per-run overrides (light / maxdim / mce / reader / crop
// booleans). The auto-discovered settings.json is loaded FIRST —
// everything else overrides it.
func addSettingsFlags(fs *flag.FlagSet) func() (*settings.Settings, error) {
	setFile := fs.String("settings", "", "JSON file with settings overrides")
	langs := fs.String("langs", "", "comma-separated languages to keep (en, ja, ...)")
	minConf := fs.Float64("min-conf", 0, "drop OCR lines below this confidence (0..1)")
	light := fs.Bool("light", false, "use the light OCR model")
	noMCE := fs.Bool("no-mce", false, "skip main-content extraction")
	maxdim := fs.Int("maxdim", 0, "extra pre-OCR downscale cap in px (0 = off)")
	readerFmt := fs.String("reader-format", "", "reader-view export: text|markdown|html")
	reader := fs.Bool("reader", false, "export the reader view (markdown)")
	cropMain := fs.Bool("crop-main", false, "crop the main-content region to <stem>.main.png")
	noOCRBoxes := fs.Bool("no-ocr-boxes", false, "omit the OCR box overlay from the HTML report")
	noMCEBoxes := fs.Bool("no-mce-boxes", false, "omit the main-content box overlay from the HTML report")
	noBoxes := fs.Bool("no-boxes", false, "omit both overlays from the HTML report")

	return func() (*settings.Settings, error) {
		s := settings.Default()
		// 1. auto-discovered settings.json — lowest precedence
		if path, err := settings.LoadAuto(s); err != nil {
			return nil, err
		} else if path != "" {
			fmt.Printf("settings: loaded %s\n", path)
		}
		// 2. explicit --settings FILE
		if *setFile != "" {
			if err := settings.LoadJSONFile(*setFile, s); err != nil {
				return nil, err
			}
		}
		if *langs != "" {
			parsed, err := settings.ParseLanguages(strings.Split(*langs, ","))
			if err != nil {
				return nil, err
			}
			s.Languages = parsed
		}
		if *minConf > 0 {
			s.MinConfidence = *minConf
		}
		// --light / --maxdim / --no-mce / --reader / --crop-main are
		// sugar; explicit --set / settings.json values win.
		if *light && s.OCRLightMode == nil {
			s.OCRLightMode = boolPtr(true)
		}
		if *maxdim > 0 && s.MaxImageDim == 0 {
			s.MaxImageDim = *maxdim
		}
		if *noMCE && !s.MCEExplicit {
			s.MCEEnabled, s.MCEExplicit = false, true
		}
		if *readerFmt != "" {
			f, err := settings.NormalizeReaderFormat(*readerFmt)
			if err != nil {
				return nil, fmt.Errorf("--reader-format: %v", err)
			}
			s.ReaderFormat = f
		}
		if *reader && s.ReaderFormat == "" {
			s.ReaderFormat = "markdown"
		}
		if *cropMain {
			s.CropMain = true
		}
		// --no-ocr-boxes / --no-mce-boxes / --no-boxes are sugar;
		// explicit --set / settings.json values win.
		if (*noOCRBoxes || *noBoxes) && s.ReportOCRBoxes == nil {
			s.ReportOCRBoxes = boolPtr(false)
		}
		if (*noMCEBoxes || *noBoxes) && s.ReportMCEBoxes == nil {
			s.ReportMCEBoxes = boolPtr(false)
		}
		return s, nil
	}
}

// extractSetPairs pulls every --set k=v out of args (both "--set k=v"
// and "--set k v" forms, repeated) and returns the pairs plus the
// remaining args. The flag package is last-wins, so --set must be
// collected before fs.Parse — this is what makes --set repeatable.
func extractSetPairs(args []string) (pairs, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--set" || a == "-set" {
			if i+1 < len(args) {
				pairs = append(pairs, args[i+1])
				i++
				continue
			}
		}
		if strings.HasPrefix(a, "--set=") || strings.HasPrefix(a, "-set=") {
			pairs = append(pairs, strings.SplitN(a, "=", 2)[1])
			continue
		}
		rest = append(rest, a)
	}
	return pairs, rest
}

func boolPtr(v bool) *bool { return &v }

func parseSettingsFlags(fs *flag.FlagSet, args []string) (*settings.Settings, error) {
	setPairs, filtered := extractSetPairs(args)
	build := addSettingsFlags(fs)
	if err := fs.Parse(filtered); err != nil {
		return nil, err
	}
	s, err := build()
	if err != nil {
		return nil, err
	}
	if err := s.ParseSetList(setPairs); err != nil {
		return nil, err
	}
	return s, nil
}

func parsePipeline(cmd string, args []string, common commonOpts) (runOptions, *settings.Settings, error) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	outdir := fs.String("o", "", "output directory (default ./results)")
	noHTML := fs.Bool("no-html", false, "write JSON only, no HTML reports")
	debug := fs.Bool("debug", false, "library debug mode")
	print := fs.Bool("print", false, "print the reader view to stdout")

	setPairs, filtered := extractSetPairs(args)
	build := addSettingsFlags(fs)
	if err := fs.Parse(filtered); err != nil {
		return runOptions{}, nil, err
	}
	s, err := build()
	if err != nil {
		return runOptions{}, nil, err
	}
	if err := s.ParseSetList(setPairs); err != nil {
		return runOptions{}, nil, err
	}
	// The demo showcases the reader view by default (markdown).
	if cmd == "demo" && s.ReaderFormat == "" {
		s.ReaderFormat = "markdown"
	}
	// pull the per-run values back out of the folded settings
	opts := runOptions{
		sources:     fs.Args(),
		outdir:      *outdir,
		light:       s.OCRLightMode != nil && *s.OCRLightMode,
		mce:         s.MCEEnabled,
		html:        !*noHTML,
		maxdim:      s.MaxImageDim,
		readerFmt:   s.ReaderFormat,
		cropMain:    s.CropMain,
		printReader: *print,
	}
	if s.DebugMode != nil {
		// engine.Options.DebugMode folds this in New()
		_ = *s.DebugMode
	}
	_ = debug // --debug folds into settings.DebugMode below for symmetry
	if *debug && s.DebugMode == nil {
		s.DebugMode = boolPtr(true)
	}
	if opts.outdir == "" {
		opts.outdir = "results"
	}
	if len(opts.sources) == 0 && cmd == "run" {
		return opts, s, fmt.Errorf("run needs at least one input file or URL")
	}
	return opts, s, nil
}

// cmdInit implements `screenai init [FILE]`: write a fully documented
// settings.json. The template contains every key with its stock
// default, so loading it is a behavioural no-op — users delete what
// they do not want and edit the rest.
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite an existing file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := "settings.json"
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	if !*force {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "screenai: %s already exists — use --force to overwrite\n", path)
			return 1
		}
	}
	if err := os.WriteFile(path, []byte(settings.Template()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 1
	}
	fmt.Printf("wrote %s — every key at its stock default; delete what you don't need\n", path)
	fmt.Printf("auto-loaded on the next run (or point $SCREENAI_SETTINGS at it)\n")
	return 0
}

func cmdInfo(eng *engine.Engine) int {
	fmt.Println("Chrome Screen AI — the real closed-source binary")
	fmt.Printf("  component dir : %s\n", eng.ComponentDir())
	fmt.Printf("  library       : libchromescreenai v%s\n", eng.VersionString())
	if err := eng.InitOCR(); err != nil {
		fmt.Printf("  OCR model     : failed to load (%v)\n", err)
	} else {
		mode := "full"
		if eng.OCRLightMode() {
			mode = "light"
		}
		fmt.Printf("  OCR model     : loaded (%s)\n", mode)
	}
	fmt.Printf("  max image dim : %d\n", eng.MaxImageDimension())
	if eff := eng.Settings().Effective(); len(eff) > 0 {
		fmt.Println("  active settings:")
		for _, kv := range eff {
			fmt.Printf("    %s\n", kv)
		}
	}
	return 0
}

func materializeExamples(outdir string) ([]string, error) {
	exdir := filepath.Join(outdir, "examples")
	if err := os.MkdirAll(exdir, 0o755); err != nil {
		return nil, err
	}
	var sources []string
	for _, name := range examples.Names() {
		data := examples.Map()[name]
		path := filepath.Join(exdir, name)
		need := true
		if info, err := os.Stat(path); err == nil && int(info.Size()) == len(data) {
			need = false
		}
		if need {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return nil, err
			}
		}
		sources = append(sources, path)
	}
	fmt.Printf("running %d bundled example(s) ...\n", len(sources))
	return sources, nil
}

func cmdRun(eng *engine.Engine, sources []string, opts runOptions, s *settings.Settings) int {
	if err := os.MkdirAll(opts.outdir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 1
	}
	popts := pipeline.Options{
		Light:          opts.light,
		MaxDim:         opts.maxdim,
		MCE:            opts.mce,
		Languages:      s.Languages,
		MinConfidence:  s.MinConfidence,
		ReaderFormat:   opts.readerFmt,
		CropMain:       opts.cropMain,
		ReportOCRBoxes: s.ReportOCRBoxes,
		ReportMCEBoxes: s.ReportMCEBoxes,
		Effective:      s.Effective(),
	}

	var entries []report.Entry
	failures := 0
	for _, source := range sources {
		fmt.Printf("[input] %s\n", source)
		inp, err := pipeline.Load(source)
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}
		var res *pipeline.Result
		var rep *pipeline.ReportData
		err = eng.Session(func(sess *engine.Session) error {
			var perr error
			res, rep, perr = pipeline.Process(sess, inp, popts)
			return perr
		})
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}
		entry, err := report.WriteResults(opts.outdir, res, rep, inp.Name, opts.html)
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			continue
		}
		entries = append(entries, entry)
		fmt.Printf("[ok] %s: %s\n", inp.Name, entry.Summary)
		if res.OCR != nil {
			fmt.Printf("      %d lines, %d words, conf %.3f\n",
				len(res.OCR.Lines), res.OCR.WordCount(), res.OCR.AvgConfidence())
		}
		if res.Filter != nil {
			fmt.Printf("      filter: %d/%d lines kept (langs %s, min-conf %g)\n",
				res.Filter.LinesKept, res.Filter.LinesBefore,
				strings.Join(res.Filter.Languages, ","), res.Filter.MinConfidence)
		}
		if rep.MCE != nil && rep.MCE.Stats != nil {
			st := rep.MCE.Stats
			fmt.Printf("      main content: %d/%d nodes (%.1f%% clutter), %d words, ~%ds read\n",
				st.SelectedNodes, st.TotalNodes, st.ClutterPct, st.ReaderWords, st.ReadingSec)
		}
		if len(rep.MainPNG) > 0 {
			fmt.Printf("      crop: %s.main.png (%d bytes)\n",
				filepath.Join(opts.outdir, report.Stem(inp.Name)), len(rep.MainPNG))
		}
		if opts.printReader && rep.ReaderView != "" {
			fmt.Printf("--- reader view (%s) ---\n%s", rep.ReaderFormat, rep.ReaderView)
		}
	}

	if len(entries) > 0 && opts.html {
		path := filepath.Join(opts.outdir, "index.html")
		if err := os.WriteFile(path, []byte(report.BuildIndex(entries)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 1
		}
		fmt.Printf("index: %s\n", path)
	}
	fmt.Printf("done: %d ok, %d failed (results in %s)\n", len(entries), failures, opts.outdir)
	if failures > 0 {
		return 1
	}
	return 0
}
