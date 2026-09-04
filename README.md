# ScreenAI (Go)

Run **Google's real, closed-source Screen AI engine** — the very
`libchromescreenai.so` / `chrome_screen_ai.dll` that Chrome itself downloads
through its component updater (component id `mfhmdacoffpmifoibamicehhklffanao`)
— on **any input format**, from a single static binary on **Linux and
Windows**:

| input | what runs | output |
|---|---|---|
| PNG, JPEG, GIF, BMP, TGA, PPM/PGM/PBM | **PerformOCR** (word-level OCR) + Screen2x main content | OCR JSON + boxes, MCE JSON, HTML report |
| HTML pages | Screen2x **ExtractMainContent** on an estimated a11y tree | reader view, main-content nodes |
| JSON page trees | Screen2x | reader view, main-content nodes |
| plain text | Screen2x | reader view, main-content nodes |

Every result is produced by the genuine Google binary through its public C
API — no substitute engines are involved.

```
┌──────────────┐   dlopen/LoadLibrary   ┌─────────────────────────────┐
│  screenai    │ ─────────────────────► │  Google Screen AI component │
│  (this repo) │   purego / syscall     │  libchromescreenai + models │
└──────────────┘   (no cgo, no toolchain) └─────────────────────────────┘
      │                                        ▲
      │ run / demo / info / settings           │ download (Chrome's public
      ▼                                        │ component-updater endpoint)
  JSON + HTML reports                    ~124 MB, fetched once
```

## Quick start

Grab a binary from [Releases](../../releases) (Linux and Windows, x64),
then:

```console
$ ./screenai download              # fetch the component from Google (~124 MB, resumable)
$ ./screenai info                  # engine status
$ ./screenai run photo.png page.html notes.txt -o results
$ ./screenai demo                   # run the bundled examples
$ ./screenai serve --port 8778      # expose the engine as an HTTP API
$ ./screenai settings               # every available setting, with defaults
$ ./screenai selftest               # verify codecs (and the engine if present)
```

Each run writes `{name}.ocr.json`, `{name}.mce.json`, a self-contained
`{name}.report.html` (image with confidence-colored OCR overlay, reader
view, MAIN badges), and an `index.html` hub.

## Settings — every knob, extracted from the binary itself

`screenai settings` prints this reference live from the binary.

**How deep settings reach a closed-source library:** the component reads
its own configuration (the Screen2x thresholds, the OCR engine graph, the
script→model registry, the detection config) through the
`SetFileContentFunctions` virtual file system — a callback pair the
wrapper registers. This wrapper intercepts those file requests and
rewrites the configs on the fly, so the genuine Google models run with
your values. Everything below marked *verified* was proven live against
the real binary (run `screenai selftest` to re-prove it on your machine):

| setting | default | mechanism | what it does |
|---|---|---|---|
| `ocr.light_mode` | `false` | C API | light (faster, less accurate) OCR model |
| `ocr.debug_mode` | `false` | C API | dump all i/o protos to the temp dir |
| `ocr.scripts` | all 21 | script registry | **target-language control** — only these script recognizers load and run (*verified*: `und,Jpan,Kore,Hani` keeps Latin OCR identical while skipping 16 models) |
| `ocr.languages` | (all) | wrapper | keep only OCR lines detected in these languages (`en`, `ja`, …); applied before MCE so the reader view honors it |
| `ocr.min_confidence` | `0` | wrapper | drop OCR lines below this confidence [0, 1] |
| `ocr.max_image_dimension` | `0` | wrapper | extra pre-OCR downscale cap (the library caps at 2048 itself) |
| `ocr.tflite_interpreter_pool_min_size` | `4` | engine graph | TFLite interpreter pool size (*verified accepted*) |
| `ocr.max_in_flight` | `1` | engine graph | parallelism through the OCR graph (*verified accepted*) |
| `ocr.detection_score_threshold` | `0.5` | detection config | text-region detection sensitivity (*verified*: 0.5→0.9 changed 25→28 OCR lines on the news example) |
| `mce.enabled` | `true` | wrapper | run Screen2x at all |
| `mce.threshold_non_essential` | `0.65` | pbtxt | **the clutter gate** — lower = keep more, higher = strip more (*verified*: 0.01 → 7 of 41 nodes, 0.65 → 8, 0.99 → 15) |
| `mce.threshold_headline` | `0.0` | pbtxt | headline class threshold |
| `mce.threshold_main` | `0.0` | pbtxt | main-content class threshold |
| `mce.num_threads` | `1` | pbtxt | Screen2x TFLite inference threads |
| `mce.chrome_post_process` | `true` | pbtxt | Chrome-specific post-processing |

Apply them in three ways:

```console
# 1. individual flags (repeatable)
$ screenai run --set mce.threshold_non_essential=0.9 --set ocr.scripts=und,Jpan,Kore,Hani photo.png

# 2. quick sugar for the common ones
$ screenai run --langs en --min-conf 0.8 scan.png

# 3. a JSON file
$ screenai run --settings my.json photo.png
```

```json
{
  "mce.threshold_non_essential": 0.9,
  "ocr.scripts": ["und", "Jpan", "Kore", "Hani"],
  "ocr.languages": ["en"],
  "ocr.min_confidence": 0.6
}
```

Script codes for `ocr.scripts`: `Arab Beng Cyrl Deva Geor Grek Gujr Guru
Hani Hebr Jpan Khmr Knda Kore Laoo Mlym Sinh Taml Telu Thai und`. Language
codes (`en`, `ru`, `ja`, …) are accepted too and mapped to scripts; Latin
languages ride on the `und` universal recognizer, which is always kept.

Notes on the knobs people usually ask about:

- **Language**: there is no "set output language" C function. Target
  languages are expressed through `ocr.scripts` (restrict what the
  pipeline loads) and `ocr.languages` (filter results by the model's own
  per-line language detection).
- **top-k / temperature do not exist** — the OCR and Screen2x models are
  deterministic; no sampling parameters appear anywhere in
  libchromescreenai's public surface, and this wrapper does not invent
  fake ones. The deep analysis behind the table above is as far as the
  knobs go.
- **Found but deliberately not exposed**: the engine graph's template
  booleans (`use_line_selection`, `use_aksara_layout`,
  `use_deep_reading_order`, …) exist in the config, but flipping them
  makes `InitOCR` fail — Google does not ship the alternative sub-models
  they gate. The per-script recognizer's CTC/beam fields similarly break
  recognition when touched. Documented, not exposed.
- Engine-level settings are **per-engine constants**: the library reads
  its configs during initialization and caches them (it cannot be
  re-initialized in-process — a second `InitOCR` aborts, verified), so
  the CLI applies them at startup. `--langs`/`--min-conf`/`--light`/
  `--maxdim`/`--no-mce` are per-run.

Other flags: `--no-html` (JSON only), `--debug`, `--component DIR`
(auto-detected otherwise), and `SCREENAI_TRACE_FILES=1` to log every file
the library requests through the VFS.

## HTTP API (`screenai serve`)

| endpoint | description |
|---|---|
| `GET /` | human-readable status page |
| `GET /api/info` | engine info (version, max image dimension, light mode) |
| `GET /api/settings` | every setting with defaults + the effective overrides |
| `POST /api/process?name=file.png&light=0\|1&maxdim=px&mce=0\|1&langs=en,ja&min_conf=0.8` | the whole pipeline; the request body is the raw file bytes; the response is the same JSON the CLI writes, plus a base64 PNG preview of exactly what OCR saw |
| `GET /api/sample` | a bundled example image for quick tests |

```console
$ curl -s --data-binary @photo.png 'http://127.0.0.1:8778/api/process?name=photo.png' | jq '.ocr.lines | length'
```

Engine-level settings are fixed while the server runs; start it with
`--set` / `--settings` (the effective values are printed at startup and
echoed by `/api/settings`). The language / confidence filters are
per-request query params. CORS is open, so browser apps can talk to it
directly.

## Building from source

```console
$ go install github.com/mehmannavaz/ScreenAI/cmd/screenai@latest
# or, from a checkout:
$ make            # ./bin/screenai
$ make test       # unit tests
$ make windows    # cross-compile bin/screenai.exe from any OS
```

The build needs **no C toolchain**: the component is loaded at runtime
through [purego](https://github.com/ebitengine/purego) (Linux) and the
standard `syscall` package (Windows), with `CGO_ENABLED=0` everywhere —
so cross-compiling `GOOS=windows` from Linux just works. On Linux, the
no-op `threadlogger` shim the component requires is generated in pure Go
(a hand-assembled 448-byte ELF64) and cached under `~/.screenai`.

## Layout

```
cmd/screenai       entry point
internal/engine    dlopen/LoadLibrary bridge, SkBitmap ABI, model-file callbacks (+ settings VFS overlay), threadlogger shim
internal/pb        protobuf wire codec (VisualAnnotation decode, ViewHierarchy encode)
internal/pagetree  page trees: OCR/HTML/JSON/text → the Screen2x input format
internal/imaging   format sniffing, decoders (TGA/PPM custom), box downscale
internal/pipeline  input classification + the OCR/MCE pipelines + post-filters
internal/report    self-contained HTML reports + index
internal/server    the HTTP API
internal/componentdl  parallel resumable component download + CRX3 extraction
internal/settings  the deep settings system: key registry + config rewriters (pbtxt, binarypb, script registry, detection config)
internal/selftest  codec/golden checks + settings verification, shipped inside the binary
internal/cli       command line interface
examples/          demo inputs (embedded into the binary)
```

## Requirements & caveats

- **x64 Linux (glibc) or x64 Windows** — that's every build of the
  component Google ships today (verified against the component updater:
  `os=android` returns HTTP 204 for all architectures; `linux/arm64`
  serves the identical x64 CRX).
- The ~124 MB component is downloaded from Google's public
  component-updater endpoint — the exact URL Chrome uses. It is Google's
  proprietary binary; this repository contains none of it.
- One engine per process: the library cannot be re-initialized
  in-process (verified), so config-file settings apply at engine
  creation — exactly how Chromium itself uses the component.
- Progressive JPEG and WebP are not supported by the pipeline's decoders
  — convert to PNG first.

## Testing

```console
$ go test ./...          # hermetic unit tests (no component needed)
$ ./screenai selftest     # same checks inside the shipped binary,
                          # plus a live engine smoke test, real-file
                          # settings patcher checks, and a live A/B
                          # proof that threshold overrides change the
                          # model's actual selection
```

The ViewHierarchy encoder is verified byte-for-byte against golden bytes
produced by the reference implementation that was validated against the
real Screen2x model, and the threadlogger shim against the golden ELF.
The settings rewriters are tested against synthetic fixtures (unit) and
against the component's real files (selftest).
