# OneTool package URL import

The URL import form includes a OneTool browse link to
`https://console.cloud.baidu-int.com/onetool/skills`. This source imports an
already-known package URL anonymously; it does not resolve a detail-page ID or
use Ugate, Skill API tokens, browser cookies, or the caller's session upstream.

Supported inputs:

- `https://mcp-proxy.baidu-int.com/api/v1/skills/download/<skill-id>/<version>/<name>.zip`
- `https://mcp-proxy.baidu-int.com/api/v1/skills/download/permanent/<uuid>/<name>.zip`
- Either URL prefixed by the platform's copied `下载并安装此Skill ` text.

Copy/paste text is parsed as input, never executed as a command. Detail-page
URLs receive a clear message asking for the package link. Anonymous access can
still be denied by OneTool; in that case use an authorized local archive upload.
This does not promise anonymous access to every restricted or private skill.

HTTPS, host, port and package paths are allowlisted. Embedded credentials,
query parameters, fragments and redirects outside those exact package routes
are rejected. Transport failures never echo proxy credentials or remote bodies.
Downloads are capped at 16 MiB. ZIP validation rejects unsafe paths, symlinks,
duplicate entries, invalid text and oversized text files/bundles. Import does
not execute package contents. Existing text-file import rules, source labels,
scope/ownership checks and duplicate-name handling remain in effect.

Origin metadata retains the exact validated URL with source type `onetool`.
Explicit source refresh uses the same credential-free package flow: a permanent
link may return a newer package, while a versioned link stays pinned. There is
no automatic update or inference of version numbers from a detail page.
