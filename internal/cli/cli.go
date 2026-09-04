// Package cli implements the screenai command line interface.
//
// Commands:
//
//	screenai run FILE...     process inputs (default when bare files are given)
//	screenai demo            run the bundled examples
//	screenai info            show library + component info
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
  demo            run the bundled examples
  info            show library + component info
  serve           serve the engine as an HTTP API (default port 8778)
  download        fetch the Screen AI component from Google (~124 MB)
  selftest        verify codecs (and the engine when present)
  version         print the version

flags (run/demo):
  -o DIR          output directory (default ./results)
  --light         use the light OCR model
  --no-mce        skip main-content extraction (images: OCR only)
  --no-html       write JSON only, no HTML reports
  --maxdim PX     extra pre-OCR downscale cap (0 = off)
  --debug         library debug mode (dumps i/o protos to tmp)

common flags:
  --component DIR Screen AI component directory (auto-detected)
  -v, -vv         more logging

examples:
  screenai download
  screenai info
  screenai run photo.png page.html notes.txt
  screenai demo
  screenai serve --port 8778

The engine is Google's genuine ScreenAI component (id
mfhmdacoffpmifoibamicehhklffanao) driven through its public C API.
OCR language is detected by the model per line; the models are
deterministic, so there are no top-k / temperature knobs anywhere in
the library's public surface.`

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
		fs.Parse(args)
		if err := server.Serve(*bind, *port, common.component); err != nil {
			fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
			return 1
		}
		return 0
	}

	// run / demo / info all need the engine.
	opts, engineOpts, err := parsePipeline(cmd, args, common)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 2
	}
	eng, err := engine.New(engineOpts)
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
	return cmdRun(eng, sources, opts)
}

type runOptions struct {
	sources []string
	outdir  string
	light   bool
	mce     bool
	html    bool
	maxdim  int
}

type commonOpts struct {
	component string
}

// knownValueFlags are the flags that take a separate value argument.
var knownValueFlags = map[string]bool{
	"o": true, "component": true, "maxdim": true,
	"port": true, "bind": true, "parts": true,
	"os": true, "arch": true, "dir": true,
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

func parsePipeline(cmd string, args []string, common commonOpts) (runOptions, engine.Options, error) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	outdir := fs.String("o", "", "output directory (default ./results)")
	light := fs.Bool("light", false, "use the light OCR model")
	noMCE := fs.Bool("no-mce", false, "skip main-content extraction")
	noHTML := fs.Bool("no-html", false, "write JSON only, no HTML reports")
	maxdim := fs.Int("maxdim", 0, "extra pre-OCR downscale cap in px (0 = off)")
	debug := fs.Bool("debug", false, "library debug mode")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, engine.Options{}, err
	}
	opts := runOptions{
		sources: fs.Args(),
		outdir:  *outdir,
		light:   *light,
		mce:     !*noMCE,
		html:    !*noHTML,
		maxdim:  *maxdim,
	}
	if opts.outdir == "" {
		opts.outdir = "results"
	}
	if len(opts.sources) == 0 && cmd == "run" {
		return opts, engine.Options{}, fmt.Errorf("run needs at least one input file or URL")
	}
	return opts, engine.Options{ComponentDir: common.component, DebugMode: *debug}, nil
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

func cmdRun(eng *engine.Engine, sources []string, opts runOptions) int {
	if err := os.MkdirAll(opts.outdir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "screenai: %v\n", err)
		return 1
	}
	popts := pipeline.Options{Light: opts.light, MaxDim: opts.maxdim, MCE: opts.mce}

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
		err = eng.Session(func(s *engine.Session) error {
			var perr error
			res, rep, perr = pipeline.Process(s, inp, popts)
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
