"""Explicit deployment opt-in for unrestricted browser WebSocket origins."""


def extend(upstream, files, entries):
    relative = "server/internal/realtime/hub.go"
    result = [(name, text) for name, text in entries if name != relative]
    override = files / relative
    text = next(
        (text for name, text in entries if name == relative),
        (override if override.is_file() else upstream / relative).read_text(),
    )
    anchor = "func checkOrigin(r *http.Request) bool {\n"
    if text.count(anchor) != 1:
        raise SystemExit("WebSocket origin policy anchor changed")
    policy = """\t// Explicit self-hosted opt-in: disables only the origin gate, not token,
\t// cookie, workspace membership or resource-scope authorization.
\tif strings.EqualFold(strings.TrimSpace(os.Getenv("MULTICA_WS_ALLOW_ALL_ORIGINS")), "true") {
\t\treturn true
\t}
"""
    result.append((relative, text.replace(anchor, anchor + policy)))
    return result
