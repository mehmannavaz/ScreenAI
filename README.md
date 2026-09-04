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
      │ run / demo / info                      │ download (Chrome's public
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
$ ./screenai selftest               # verify codecs (and the engine if present)
```

Each run writes `{name}.ocr.json`, `{name}.mce.json`, a self-contained
`{name}.report.html` (image with confidence-colored OCR overlay, reader
view, MAIN badges), and an `index.html` hub.

## Settings

| flag | what it does |
|---|---|
| `--light` | switch to the light OCR model (`SetOCRLightMode`) |
| `--maxdim N` | extra pre-OCR downscale cap in pixels (the library caps at 2048 itself) |
| `--no-mce` | skip main-content extraction (images: OCR only) |
| `--no-html` | write JSON only |
| `--debug` | library debug mode (dumps I/O protos to tmp) |
| `--component DIR` | use a specific component directory (auto-detected otherwise) |

Notes on the knobs people usually ask about:

- **Language** is detected by the model per OCR line (see the `language`
  field in every line of the OCR JSON); there is no manual language hint
  in the library's public API.
- **top-k / temperature do not exist** — the OCR and Screen2x models are
  deterministic; no sampling parameters appear anywhere in
  libchromescreenai's public surface, and this wrapper does not invent
  fake ones.

## HTTP API (`screenai serve`)

| endpoint | description |
|---|---|
| `GET /` | human-readable status page |
| `GET /api/info` | engine info (version, max image dimension, light mode) |
| `POST /api/process?name=file.png&light=0\|1&maxdim=px&mce=0\|1` | the whole pipeline; the request body is the raw file bytes; the response is the same JSON the CLI writes, plus a base64 PNG preview of exactly what OCR saw |
| `GET /api/sample` | a bundled example image for quick tests |

```console
$ curl -s --data-binary @photo.png 'http://127.0.0.1:8778/api/process?name=photo.png' | jq '.ocr.lines | length'
```

CORS is open, so browser apps can talk to it directly.

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
internal/engine    dlopen/LoadLibrary bridge, SkBitmap ABI, model-file callbacks, threadlogger shim
internal/pb        protobuf wire codec (VisualAnnotation decode, ViewHierarchy encode)
internal/pagetree  page trees: OCR/HTML/JSON/text → the Screen2x input format
internal/imaging   format sniffing, decoders (TGA/PPM custom), box downscale
internal/pipeline  input classification + the OCR/MCE pipelines
internal/report    self-contained HTML reports + index
internal/server    the HTTP API
internal/componentdl  parallel resumable component download + CRX3 extraction
internal/selftest  codec/golden checks shipped inside the binary
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
- Progressive JPEG and WebP are not supported by the pipeline's decoders
  — convert to PNG first.

## Testing

```console
$ go test ./...          # hermetic unit tests (no component needed)
$ ./screenai selftest     # same checks inside the shipped binary,
                          # plus a live engine smoke test when the
                          # component is present
```

The ViewHierarchy encoder is verified byte-for-byte against golden bytes
produced by the reference implementation that was validated against the
real Screen2x model, and the threadlogger shim against the golden ELF.
