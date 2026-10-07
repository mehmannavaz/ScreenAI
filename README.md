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
$ ./screenai run --reader --crop-main article.png   # reader view + main-content crop
$ ./screenai run --no-boxes article.png            # HTML report without overlays
$ ./screenai demo                   # run the bundled examples
$ ./screenai init                   # write a documented settings.json
$ ./screenai serve --port 8778      # expose the engine as an HTTP API
$ ./screenai settings               # every available setting, with defaults
$ ./screenai selftest               # verify codecs (and the engine if present)
```

Each run writes `{name}.ocr.json`, `{name}.mce.json`, a self-contained
`{name}.report.html` (the image with **overlay layers highlighting
everything the models detected**, reader view, outline, stats, MAIN
badges), and an `index.html` hub. With
`--reader` it also writes `{name}.reader.md` (or `.txt` / `.html`), and
with `--crop-main` the `{name}.main.png` crop of the main-content region.

## Semantic page tree — the roles Screen2x actually eats

Deep analysis of `screen2x_config.pbtxt` shows the main-content model
is a **message-passing GNN** with a dedicated categorical
`chrome_role` input (and a `chrome_role_description` string) plus
numeric features per node. Chrome's own accessibility trees always
feed it rich roles — `heading` with `hierarchical_level`, `link`,
`list`, `listItem`, `banner`, `complementary`… — while a naive OCR
wrapper feeds it `rootWebArea/paragraph/staticText` and starves the
model's main structural feature.

This wrapper **reconstructs the roles Chrome would send** from signals
the OCR annotation itself carries:

| role | detected from | notes |
|---|---|---|
| `heading` + `hierarchical_level` | line height vs the area-weighted body size (ratio ≥ `tree.heading_min_ratio`) | h1 for ~2× body, h2 ~1.6×, …; wrapped headline lines merge; weight 700 |
| `list` / `listItem` | bullet/number markers (`•`, `1.`, `a)`…) **and** horizontal runs of short same-row lines (nav bars, footer link rows) | wrapped item lines join their item |
| `link` | the engine's per-word `foreground_rgb` (link blue/purple ink) or a solid underline row in the pixels | packed `0xRRGGBB00`, no guessing |
| `complementary` | narrow side columns interleaved with main-column content | Chrome's `aside` landmark |
| `contentinfo` | trailing strip after a ≥2.5 body-height gap | Chrome's `footer` landmark |
| `separator` | empty thin wide lines / `content_type: separator` | rule lines, not empty text nodes |
| `image` | `content_type: image` lines | |

Paragraph lines merge across OCR blocks on real typography signals
(vertical gap ≤ `tree.paragraph_gap` × body height, left alignment,
size, column overlap) — so paragraphs contain several `staticText`
children exactly like Chrome's trees, instead of the
degenerate `paragraph = staticText` duplication. The builder also
emits `/extras/styles/text-align` (a string attribute Chrome's
convertor sends) for centered content. HTML inputs get the same
treatment natively: `<a>`/`<strong>`/`<em>`/`<code>` become real
`link`/`strong`/`emphasis` nodes inside paragraphs, and markdown text
(`#`, bullets, `>` quotes) maps to headings/lists/blockquotes.

Every detector is a setting (`tree.*` group) and a flag:

```sh
screenai run --no-links page.png           # keep links as plain text
screenai run --heading-ratio 1.6 page.png   # only clear display sizes
screenai run --set tree.detect_regions=false page.png
```

| setting | default | mechanism | what it does |
|---|---|---|---|
| `tree.detect_headings` | `true` | wrapper | mark clearly larger lines as `heading` + level |
| `tree.detect_lists` | `true` | wrapper | bullet/numbered lines and nav rows → `list`/`listItem` |
| `tree.detect_links` | `true` | wrapper | link-coloured / underlined lines → `link` |
| `tree.detect_regions` | `true` | wrapper | side columns → `complementary`, trailing strip → `contentinfo` |
| `tree.heading_min_ratio` | `1.3` | wrapper | height ratio over body that makes a heading |
| `tree.paragraph_gap` | `0.75` | wrapper | max line gap (× body height) merging into one paragraph |

## MCE abilities beyond the ids

`ExtractMainContent` returns selected node ids, but the wrapper turns
that selection into a full main-content feature set — all derived from
what the Screen2x model actually picked, never invented:

- **Reader view** (`--reader`, setting `reader.format`): the distilled
  main content, exported as **markdown** (headings keep their h1–h6
  levels — including on **image inputs**, via the semantic tree; list
  items render as bullets), **text** (underlined headings) or **html**
  (`<article>` with semantic tags). `--print` dumps it to stdout; the
  server returns it as `reader_view`.
- **Outline**: every heading inside the main content, in reading order
  (`outline` in the MCE JSON; also shown in the HTML report).
- **Per-node content type**: every node carries `content_type`
  `headline | main | nonessential` — the model itself predicts three
  classes (`CONTENT_NON_ESSENTIAL`, `CONTENT_TEXT_HEADLINE`,
  `CONTENT_TEXT_MAIN` in screen2x_config.pbtxt), but the C API exposes
  only the selection, so the headline/main split is derived from the
  heading roles (documented, not invented).
- **Reading order**: selected nodes carry `order` (1-based, tree
  order) — the sequence the reader view emits.
- **Stats** (`stats` in the MCE JSON): selected/total nodes, kept
  ratio, clutter dropped %, reader words, chars, reading time
  (200 wpm), headline count and the selected-role mix
  (`selected_by_role`).
- **Per-node selection**: every node in the MCE JSON carries
  `selected: true/false`, plus `level` and `font_size`.
- **Landmark veto** (`mce.exclude_regions`, default `true`):
  Screen2x occasionally keeps sidebar ads or copyright strips; nodes
  selected inside `complementary`/`contentinfo` landmarks are demoted
  from the reader view/stats/crop (the raw model selection stays in
  `content_axnode_ids`, the demoted ids in `vetoed_axnode_ids`).
  `--keep-regions` opts out.
- **Main-content crop** (`--crop-main`, setting `mce.crop_main`):
  images get `{name}.main.png` — the union bounding box of the
  selection cropped pixel-exact from the image the OCR saw (4px
  padding, clamped); the server returns it as `main_png_base64`, and
  the exact box is always in `crop_box`.

## HTML report overlays — see exactly what was done

The HTML report embeds the exact image the models processed and draws
**two overlay layers** on top of it, so you can see what was done, not
just read about it:

- **OCR layer**: one box per recognized line, border color encoding
  the model's confidence for that line (green ≥ .95, blue ≥ .80,
  yellow ≥ .60, red below). Hover a box for its text and confidence.
- **Main-content layer**: a filled green box per Screen2x-selected
  node — **amber when the node is headline content** (a heading) —
  plus a dashed box around the whole main-content region: the exact
  region `--crop-main` crops. Hover any box for role, level, content
  type and text.

Both layers have a toggle pill above the image (click to flip a layer
on/off while reading — no regeneration needed), and each layer can be
switched off at generation time, in which case it is not embedded in
the file at all:

```sh
screenai run --no-ocr-boxes photo.png   # image + MCE boxes only
screenai run --no-mce-boxes photo.png   # image + OCR boxes only
screenai run --no-boxes photo.png       # plain image, no overlays
screenai run --set report.ocr_boxes=false photo.png   # same, via --set
```

| setting | default | mechanism | what it does |
|---|---|---|---|
| `report.ocr_boxes` | `true` | wrapper | embed the OCR line-box overlay layer in the HTML report |
| `report.mce_boxes` | `true` | wrapper | embed the Screen2x main-content overlay layer + region box |

They follow the normal precedence chain (settings.json → `--set` →
sugar flags), so a settings.json with `"report": {"ocr_boxes": false}`
can be re-enabled per run with `--set report.ocr_boxes=true`.

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
| `mce.exclude_regions` | `true` | wrapper | demote selections inside `complementary`/`contentinfo` landmarks — sidebar ads and copyright strips stay out of the reader view; raw ids preserved in `vetoed_axnode_ids` |
| `mce.crop_main` | `false` | wrapper | crop the main-content region out of the image → `<stem>.main.png` + `crop_box` (images only) |
| `reader.format` | `none` | wrapper | export the reader view: `text` \| `markdown` \| `html` → `<stem>.reader.*` |
| `report.ocr_boxes` | `true` | wrapper | embed the OCR line-box overlay layer in the HTML report (see [HTML report overlays](#html-report-overlays--see-exactly-what-was-done)) |
| `report.mce_boxes` | `true` | wrapper | embed the main-content overlay layer + dashed region box in the HTML report |
| `tree.detect_headings` | `true` | wrapper | rebuild `heading` + `hierarchical_level` roles from OCR line sizes (see [Semantic page tree](#semantic-page-tree--the-roles-screen2x-actually-eats)) |
| `tree.detect_lists` | `true` | wrapper | bullets/numbered lines and same-row nav runs → `list`/`listItem` |
| `tree.detect_links` | `true` | wrapper | link-inked (engine `foreground_rgb`) or underlined lines → `link` |
| `tree.detect_regions` | `true` | wrapper | side columns → `complementary` (aside), trailing strip → `contentinfo` (footer) |
| `tree.heading_min_ratio` | `1.3` | wrapper | height ratio over body text that makes a heading |
| `tree.paragraph_gap` | `0.75` | wrapper | max line gap (× body height) that merges lines into one paragraph |

### settings.json

`./settings.json` (or the file `$SCREENAI_SETTINGS` points at) is
**auto-loaded** whenever it exists — `screenai init` writes a fully
documented one (`settings.example.json` in this repo is the same file).
Both flat and nested formats work, `_`-prefixed keys are documentation,
and empty values mean "unset":

```json
{
  "mce": {
    "threshold_non_essential": 0.9,
    "crop_main": true
  },
  "ocr": {
    "languages": ["en"],
    "min_confidence": 0.6
  },
  "reader": { "format": "markdown" },
  "report": { "mce_boxes": false }
}
```

Precedence, lowest to highest: defaults → auto-loaded `settings.json`
→ `--settings FILE` → `--set k=v` (and flag sugar). A file that fails
  to parse stops the run — a typo'd settings.json must never silently
  run the engine with half the intended overrides. `screenai settings
  --json` prints the machine-readable reference plus the template.

Apply settings in three ways:

```console
# 1. individual flags (repeatable)
$ screenai run --set mce.threshold_non_essential=0.9 --set ocr.scripts=und,Jpan,Kore,Hani photo.png

# 2. quick sugar for the common ones
$ screenai run --langs en --min-conf 0.8 --reader --crop-main scan.png

# 3. a JSON file
$ screenai run --settings my.json photo.png   # or ./settings.json, auto-loaded
```

`my.json` may use the flat form too — both are the same file format:

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
| `POST /api/process?name=file.png&light=0\|1&maxdim=px&mce=0\|1&langs=en,ja&min_conf=0.8&reader=markdown\|text\|html&crop=0\|1` | the whole pipeline; the request body is the raw file bytes; the response is the same JSON the CLI writes, plus a base64 PNG preview of exactly what OCR saw, the `reader_view` export, and — with `crop=1` — `main_png_base64` |
| `GET /api/sample` | a bundled example image for quick tests |

```console
$ curl -s --data-binary @photo.png 'http://127.0.0.1:8778/api/process?name=photo.png&reader=markdown&crop=1' \
    | jq '{lines: (.ocr.lines|length), reader: .reader_view[0:80], crop: (.main_png_base64|length)}'
```

Engine-level settings are fixed while the server runs; start it with
`--set` / `--settings` (a `settings.json` in the server's working
directory is auto-loaded too; the effective values are printed at
startup and echoed by `/api/settings`). The language / confidence
filters, the reader export, and the crop are per-request query params.
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
internal/engine    dlopen/LoadLibrary bridge, SkBitmap ABI, model-file callbacks (+ settings VFS overlay), threadlogger shim
internal/pb        protobuf wire codec (VisualAnnotation decode, ViewHierarchy encode)
internal/pagetree  page trees: OCR/HTML/JSON/text → the Screen2x input format
internal/imaging   format sniffing, decoders (TGA/PPM custom), box downscale
internal/pipeline  input classification + the OCR/MCE pipelines + post-filters + reader-view rendering + main-content crop
internal/report    self-contained HTML reports + index
internal/server    the HTTP API
internal/componentdl  parallel resumable component download + CRX3 extraction
internal/settings  the deep settings system: key registry + config rewriters (pbtxt, binarypb, script registry, detection config) + settings.json (nested/flat, auto-discovery, template)
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
                          # settings patcher checks, a live A/B
                          # proof that threshold overrides change the
                          # model's actual selection, settings.json
                          # template/nested-format checks, reader-view
                          # rendering, the main-content crop, and the
                          # HTML report overlay layers
```

The ViewHierarchy encoder is verified byte-for-byte against golden bytes
produced by the reference implementation that was validated against the
real Screen2x model, and the threadlogger shim against the golden ELF.
The settings rewriters are tested against synthetic fixtures (unit) and
against the component's real files (selftest).
