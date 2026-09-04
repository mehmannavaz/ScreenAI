package report

import "html/template"

const baseCSS = `
:root{color-scheme:dark}
*{box-sizing:border-box}
body{font-family:system-ui,-apple-system,'Segoe UI',Roboto,sans-serif;background:#101418;color:#e8eaed;margin:0;padding:28px;line-height:1.5}
a{color:#8ab4f8;text-decoration:none} a:hover{text-decoration:underline}
.wrap{max-width:1080px;margin:0 auto}
h1{font-size:22px;margin:0 0 6px;font-weight:600}
h2{font-size:18px;margin:34px 0 10px;font-weight:600;letter-spacing:.2px}
h2 .tag,h3{font-weight:500}
p{color:#c3c7cb;margin:6px 0}
code{background:#1f242c;padding:2px 6px;border-radius:4px;color:#8ab4f8;font-size:.92em}
.card{background:#171b21;border:1px solid #232a33;border-radius:12px;padding:16px 18px;margin:14px 0}
.chips{display:flex;gap:8px;flex-wrap:wrap;margin:12px 0}
.chip{background:#1b2129;border:1px solid #2a3341;border-radius:999px;padding:4px 12px;font-size:13px;color:#c3c7cb}
.tag{font-size:11px;font-weight:600;letter-spacing:.6px;border-radius:4px;padding:2px 7px;vertical-align:2px;margin-left:8px}
.tag.ocr{background:#1a3a2a;color:#7ee2a8;border:1px solid #2b5a41}
.tag.mce{background:#1a2a3a;color:#8ab4f8;border:1px solid #2b4160}
table{border-collapse:collapse;width:100%;font-size:13.5px}
th,td{text-align:left;padding:6px 10px;border-bottom:1px solid #232a33;vertical-align:top}
th{color:#9aa0a6;font-weight:500;font-size:12px;text-transform:uppercase;letter-spacing:.5px}
td.mono,th.mono{font-family:ui-monospace,'Cascadia Code',Consolas,monospace;color:#9aa0a6;white-space:nowrap}
pre{background:#0d1117;border:1px solid #232a33;border-radius:8px;padding:14px;overflow:auto;white-space:pre-wrap;font-size:13px;font-family:ui-monospace,'Cascadia Code',Consolas,monospace;color:#d5d9de}
.stats{display:flex;gap:10px;flex-wrap:wrap;margin:12px 0}
.stat{background:#171b21;border:1px solid #232a33;border-radius:8px;padding:8px 14px;min-width:110px}
.stat .k{font-size:11px;color:#9aa0a6;text-transform:uppercase;letter-spacing:.5px}
.stat .v{font-size:16px;font-weight:600;margin-top:2px}
.imgstage{position:relative;display:inline-block;max-width:100%;border:1px solid #232a33;border-radius:8px;overflow:hidden;line-height:0}
.imgstage img{max-width:100%;image-rendering:auto}
.overlay-box{position:absolute;border:1.5px solid #34a853;border-radius:2px;opacity:.85;mix-blend-mode:screen;cursor:crosshair}
.overlay-box:hover{background:rgba(138,180,248,.25)}
.badge{font-size:10px;font-weight:700;border-radius:3px;padding:1px 6px;letter-spacing:.5px}
.badge.main{background:#1a3a2a;color:#7ee2a8;border:1px solid #2b5a41}
.badge.off{color:#3c4043}
.reader{background:#15191f;border:1px solid #232a33;border-radius:8px;padding:18px 24px}
.reader .meta{color:#9aa0a6;font-size:12.5px;border-bottom:1px solid #232a33;padding-bottom:8px;margin-bottom:10px}
.reader h3{margin:14px 0 4px;font-size:16.5px}
.reader p{margin:6px 0;color:#d5d9de}
.verdict{border-left:4px solid #7ee2a8}
.verdict .big{font-size:17px;font-weight:600;margin-bottom:4px}
details{margin:22px 0}
summary{cursor:pointer;color:#9aa0a6;font-size:14px}
.cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:14px}
.cardlink{display:block;background:#171b21;border:1px solid #232a33;border-radius:12px;padding:16px 18px;color:inherit;transition:border-color .15s}
.cardlink:hover{border-color:#3b4757;text-decoration:none}
.cardlink h3{margin:0 0 6px;font-size:15.5px;font-weight:600}
.cardlink .meta{color:#9aa0a6;font-size:12.5px;margin:2px 0}
.cardlink .desc{color:#c3c7cb;font-size:13px;margin-top:8px}
.back{display:inline-block;margin-bottom:18px;font-size:13.5px}
footer{margin-top:36px;color:#5f6368;font-size:12px;border-top:1px solid #232a33;padding-top:14px}
`

var pageShell = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>` + baseCSS + `</style>
</head><body><div class="wrap">
{{if .Back}}<a class="back" href="index.html">&larr; all results</a>{{end}}
{{template "body" .}}
<footer>Produced by <b>ScreenAI</b> — Google's genuine closed-source Screen AI component (libchromescreenai.so / chrome_screen_ai.dll, id mfhmdacoffpmifoibamicehhklffanao) through its public C API. No substitute engines are involved.</footer>
</div></body></html>`

var reportTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"mul": func(a, b int) int { return a * b },
}).Parse(
	`{{define "body"}}
<h1>{{.Title}}</h1>
<p class="sub">{{.Subtitle}}</p>
{{if .Chips}}<div class="chips">{{range .Chips}}<span class="chip">{{.}}</span>{{end}}</div>{{end}}

{{if .HasOCR}}
<h2>OCR <span class="tag ocr">PerformOCR</span></h2>
<div class="stats">
  <div class="stat"><div class="k">lines</div><div class="v">{{.OCR.LineCount}}</div></div>
  <div class="stat"><div class="k">words</div><div class="v">{{.OCR.WordCount}}</div></div>
  <div class="stat"><div class="k">avg confidence</div><div class="v">{{.OCR.AvgConf}}</div></div>
  <div class="stat"><div class="k">languages</div><div class="v">{{if .OCR.Languages}}{{.OCR.Languages}}{{else}}&mdash;{{end}}</div></div>
</div>

{{if .OCR.HasImage}}
<div class="card"><b>{{if .OCR.HasBoxes}}Word &amp; line boxes{{else}}Input image{{end}}</b>
{{if .OCR.HasBoxes}} — box color encodes confidence (green &ge; .95, blue &ge; .80, yellow &ge; .60, red below). Hover a box for its text.{{else}} — the OCR model found no text in this image.{{end}}
<div style="margin-top:12px">
<div class="imgstage"><img src="{{.OCR.ImageURI}}" alt="input">
{{range .OCR.Boxes}}<div class="overlay-box" title="{{.Title}}" style="{{.Style}}border-color:{{.Color}}"></div>{{end}}
</div></div></div>
{{end}}

<div class="card"><b>Recognized text</b><pre>{{if .OCR.PlainText}}{{.OCR.PlainText}}{{else}}(no text detected){{end}}</pre></div>

{{if .OCR.Lines}}
<div class="card"><b>Lines</b><table>
<tr><th>#</th><th>box</th><th>conf</th><th>lang</th><th>block</th><th>text</th></tr>
{{range .OCR.Lines}}<tr><td>{{.N}}</td><td class="mono">{{.Box}}</td><td style="color:{{.Color}}">{{.Conf}}</td><td>{{.Lang}}</td><td>{{.Block}}</td><td>{{.Text}}</td></tr>
{{end}}</table></div>
{{end}}
{{end}}

{{if .HasMCE}}
<h2>Main Content <span class="tag mce">ExtractMainContent</span></h2>
{{if not .MCE.HasSelected}}
<div class="card verdict" style="border-color:#fdd663">
<div class="big" style="color:#fdd663">No main content detected</div>
The Screen2x model did not classify any node of this input as main content.</div>
{{else}}
<div class="card verdict">
<div class="big">Main content: {{.MCE.MainRole}}{{if .MCE.MainText}} &mdash; &ldquo;{{.MCE.MainText}}{{if gt (len .MCE.MainText) 90}}&hellip;{{end}}&rdquo;{{end}}</div>
<div>Chrome's <code>ComputeMainNode</code> rule (lowest common ancestor of the first and last selected nodes) resolves to this container. <b>{{.MCE.Selected}}</b> of <b>{{.MCE.Total}}</b> nodes were classified as main content.</div></div>

<div class="stats">
  <div class="stat"><div class="k">selected nodes</div><div class="v">{{.MCE.Selected}} <small>/ {{.MCE.Total}}</small></div></div>
  <div class="stat"><div class="k">clutter dropped</div><div class="v">{{if .MCE.ClutterPct}}{{.MCE.ClutterPct}}{{else}}&mdash;{{end}}</div></div>
  <div class="stat"><div class="k">reader words</div><div class="v">{{if .MCE.ReaderWords}}{{.MCE.ReaderWords}} <small>&middot; ~{{.MCE.ReadingSec}}s</small>{{else}}&mdash;{{end}}</div></div>
  <div class="stat"><div class="k">main node role</div><div class="v">{{if .MCE.MainRole}}{{.MCE.MainRole}}{{else}}&mdash;{{end}}</div></div>
  <div class="stat"><div class="k">model</div><div class="v" style="font-size:13px">screen2x Chrome a11y</div></div>
</div>

{{if .MCE.Reader}}
<div class="card"><b>Reader view</b> — distilled from the selected nodes
<div class="reader" style="margin-top:12px">
{{if .MCE.ReaderMeta}}<div class="meta">{{.MCE.ReaderMeta}}</div>{{end}}
{{range .MCE.Reader}}{{if .IsHeading}}<h3>{{.Text}}</h3>{{else}}<p>{{.Text}}</p>{{end}}{{end}}
</div></div>
{{end}}

{{if .MCE.Outline}}
<div class="card"><b>Outline</b> — headings inside the main content
<div style="margin-top:10px">
{{range .MCE.Outline}}<div style="padding-left:{{mul .Level 14}}px"><span class="mono" style="color:#8ab4f8">h{{.Level}}</span> {{.Text}}</div>{{end}}
</div></div>
{{end}}

{{if .MCE.Nodes}}
<div class="card"><b>Nodes</b><table>
<tr><th></th><th>axnode_id</th><th>role</th><th>box</th><th>text</th></tr>
{{range .MCE.Nodes}}<tr><td>{{if .IsMain}}<span class="badge main">MAIN</span>{{else}}<span class="badge off">&middot;</span>{{end}}</td><td class="mono">{{.AXNode}}</td><td>{{.Role}}</td><td class="mono">{{.Box}}</td><td>{{.Text}}</td></tr>
{{end}}</table></div>
{{end}}
{{end}}
{{end}}

{{if .RawJSON}}
<details><summary>raw JSON (as also written to the .json result files)</summary><pre>{{.RawJSON}}</pre></details>
{{end}}
{{end}}` + pageShell))

var indexTemplate = template.Must(template.New("page").Parse(
	`{{define "body"}}
<h1>Chrome Screen AI &mdash; results</h1>
<p>{{len .Entries}} input(s) processed with the real closed-source Screen AI binary &middot; {{.Stamps}}</p>
<div class="cards">
{{range .Entries}}<a class="cardlink" href="{{.HTML}}">
<h3>{{.Stem}}{{if .HasOCR}} <span class="tag ocr">OCR</span>{{end}}{{if .HasMCE}} <span class="tag mce">MCE</span>{{end}}</h3>
<div class="meta">{{.Format}} &middot; {{.Kind}} &middot; {{printf "%.1f" .Elapsed}}s</div>
<div class="desc">{{.Summary}}</div></a>
{{end}}
</div>
{{end}}` + pageShell))
