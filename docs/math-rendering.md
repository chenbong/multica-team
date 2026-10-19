# Task and chat formulas

The canonical `RichContent` renderer already includes remark-math and KaTeX.
This overlay additionally accepts complete LaTeX `\(...\)` inline formulas and
`\[...\]` display formulas by lowering them to the existing explicit-dollar
syntax before linkification and highlighting. The stored Markdown is unchanged.
Descriptions, comments and both streaming/persisted chat share this renderer.

The read-only transform uses CommonMark source ranges to leave fenced/indented
code, inline code, links, images, HTML literals and existing dollar math intact.
Unfinished pairs remain source until complete. Single-dollar parsing stays
disabled so ordinary prices do not become formulas. KaTeX's existing sanitizing,
untrusted-command policy and invalid-expression fallback are unchanged.

The helper is copied by `overlays/web-direct`; the checked renderer wiring lives
in `overlays/math-rendering-web-patch.py`, composed through `overlays/web-patch.py`.
Do not edit the `multica` submodule or add a second task-only Markdown renderer.
No server, database, client daemon or production deployment change is required.
