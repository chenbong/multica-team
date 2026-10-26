"""Narrow opt-ins for API CORS and attachment preview frame origins."""


def extend(upstream, files, entries):
    paths = {
        "server/cmd/server/router.go",
        "server/internal/handler/file.go",
        "server/internal/handler/avatar.go",
        "server/internal/middleware/csp.go",
    }
    result = [(name, text) for name, text in entries if name not in paths]
    for relative in sorted(paths):
        override = files / relative
        text = next(
            (text for name, text in entries if name == relative),
            (override if override.is_file() else upstream / relative).read_text(),
        )
        if relative.endswith("router.go"):
            edits = [
                ("r.Use(cors.Handler(cors.Options{", "r.Use(cors.Handler(middleware.CORSOriginPolicy(cors.Options{"),
                ("\t\tMaxAge:           300,\n\t}))", "\t\tMaxAge:           300,\n\t})))"),
            ]
        elif relative.endswith("file.go"):
            edits = [
                ('\t"github.com/multica-ai/multica/server/internal/storage"', '\t"github.com/multica-ai/multica/server/internal/middleware"\n\t"github.com/multica-ai/multica/server/internal/storage"'),
                ('func attachmentPreviewCSPHeader(frameAncestors []string) string {\n', 'func attachmentPreviewCSPHeader(frameAncestors []string) string {\n\treturn previewCSPHeader(frameAncestors, middleware.AttachmentFrameAncestorsUnrestricted())\n}\n\nfunc previewCSPHeader(frameAncestors []string, unrestricted bool) string {\n'),
                ('\tancestors := []string{"\'self\'"}\n', '\tancestors := []string{"\'self\'"}\n\tif unrestricted {\n\t\tancestors = []string{"*"}\n\t\tframeAncestors = nil\n\t}\n'),
            ]
        elif relative.endswith("avatar.go"):
            edits = [
                ('\th.setAttachmentPreviewSecurityHeaders(w)\n', '\t// Avatar framing is independent from attachment preview opt-ins.\n\tw.Header().Set("Content-Security-Policy", previewCSPHeader(h.cfg.AttachmentFrameAncestors, false))\n'),
            ]
        else:
            edits = [
                ("\t\treturn attachmentPreviewCSPHeader\n", '\t\tif AttachmentFrameAncestorsUnrestricted() {\n\t\t\treturn strings.Replace(attachmentPreviewCSPHeader, "frame-ancestors \'self\'", "frame-ancestors *", 1)\n\t\t}\n\t\treturn attachmentPreviewCSPHeader\n'),
            ]
        for before, after in edits:
            if text.count(before) != 1:
                raise SystemExit("HTTP origin policy anchor changed: " + relative)
            text = text.replace(before, after)
        result.append((relative, text))
    return result
