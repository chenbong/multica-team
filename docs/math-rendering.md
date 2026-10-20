# Task and chat formulas

The canonical `RichContent` renderer already includes remark-math and KaTeX.
This overlay additionally accepts complete single-line `$...$` inline formulas,
LaTeX `\(...\)` inline formulas and
`\[...\]` display formulas by lowering them to the existing explicit-dollar
syntax before linkification and highlighting. The stored Markdown is unchanged.
Descriptions, comments and both streaming/persisted chat share this renderer.

The read-only transform uses CommonMark source ranges to leave fenced/indented
code, inline code, links, images, HTML literals and existing double-dollar math intact.
Unfinished pairs remain source until complete. Single-dollar parsing stays
disabled in remark-math; the preprocessor converts complete single-dollar pairs
to explicit inline math only when neither edge has whitespace, there is no
newline, and the closing dollar is not followed by a digit. This preserves
ordinary `$100 ... $200`, `$100~$120`, and escaped `\$` prices. Unavoidably
ambiguous prose that looks exactly like math should escape literal dollars.
Adjacent Chinese text, underscores, asterisks and table formulas are supported.
KaTeX's bundled `mhchem` extension is loaded by the canonical read-only renderer
for `\ce{...}` chemical equations; no external service is required.
KaTeX's existing sanitizing,
untrusted-command policy and invalid-expression fallback are unchanged.

The helper is copied by `overlays/web-direct`; the checked renderer wiring lives
in `overlays/math-rendering-web-patch.py`, composed through `overlays/web-patch.py`.
Do not edit the `multica` submodule or add a second task-only Markdown renderer.
Only the generated frontend needs rebuilding/deployment; no backend binary,
database migration or client daemon update is needed.

`latex-samples.md` contains the user's 18-section compatibility sample. Its
component test checks expected formula counts and reparses every resulting TeX
annotation with errors enabled, so red unsupported-command fallbacks are not
mistaken for successful rendering. Helper tests protect currency/literal ranges;
component tests also cover persisted and both streaming densities.
