# Resource Web panels

## User experience

There are two shared collections: workspace Web panels and task Web panels.
Adding an HTTP(S) URL pins the panel directly to that resource. The URL field
comes first; the optional page name follows and defaults to the complete URL
when blank. Workspace
panels appear under "Workspace panels" in the workspace sidebar; task panels
appear under "Issue panels" in the task's information sidebar (Chinese:
"工作区面板" / "任务面板"). There is no personal dashboard, POPO-only category, or
separate panel library.

Clicking opens one non-modal panel on the right, without blocking the main page.
Its header switches between workspace/current-task panels, opens the original
URL in a new tab, refreshes the current embedded page, or closes the dock.
Refresh reloads that page's saved URL, resetting its unsubmitted input and
loading/error state, without reloading the task or other visited frames. The
action stays available even if a navigation never finishes loading.
Drag its left edge to resize; keyboard
Left/Right adjusts the width and Home/End chooses the minimum/maximum. Opening a
panel temporarily hides the task information sidebar; closing restores its
previous layout.

Already visited frames remain mounted while switching or hiding the dock, so
an external page can retain its input and scroll position during that visit.
Leaving a task releases that task's frames. Leaving the workspace or changing
accounts releases all frames. Refreshing does not restore embedded page state
or automatically reopen a panel. Only width is persisted, isolated by account
and workspace through the platform storage adapter.

## Sharing and authorization

- Workspace members can read and add workspace panels.
- Task access follows the existing workspace-member/task authorization path.
  Members who can edit the task can add its panels.
- Only the panel creator or workspace owner/admin can edit or remove a panel.
- Human management endpoints enforce authorization for every read and write.
  Task credentials have a separate add-only entry, described below; cloud-node
  credentials cannot use it. Workspace panels remain human-only.
- Shared changes invalidate resource-specific queries through workspace events.
  Reconnect and permission/task changes recheck the lists. An authoritative
  authorization failure removes cached frames; a transient server error does not
  silently discard an external form.
- Transferred tasks derive panel access from the task's current workspace.
  Deleting a task or workspace deletes its panel metadata in the same database
  statement, without deleting anything on the external website.
  Workspace teardown cleans both panel scopes in `DeleteWorkspaceIssueRoots`,
  using the same statement snapshot as issue deletion. The final workspace
  sweep alone is too late to discover issue-owned panels once issues are gone.
  The enclosing transaction and existing resource locks retain rollback and
  concurrent-write isolation; transferred issues are scoped by their current
  workspace, not their panel's creator.

Existing orphan metadata is not silently removed by the forward fix. Operators
can audit missing owners in a read-only transaction:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/resource-web-panel-orphans.sql
```

The report contains identifiers and timestamps, not page names or
URLs that may contain sensitive input. Review and back up any historical rows
before a separately authorized, identifier-scoped cleanup; do not delete panels
because their creator left or their issue moved to another live workspace.

## External-page boundary

The user hosts the external application (POPO or any other Web service). Team
Mesh stores only its name and URL; it does not host, proxy, or fetch that page on
the server. The exact query string and fragment are retained. No workspace,
task, user identifier, token, or parent referrer is appended to frame navigation.
Do not put secrets in shared URLs: every authorized resource member can read
them, and external-site authentication is still governed by that site/browser.

Frames are sandboxed without parent navigation or modal-dialog permissions.
Pages using the same hostname as Team Mesh (including a different port) are
offered as new-tab links rather than embedded, because cookies are hostname-
scoped. HTTP pages also use the new-tab fallback under an HTTPS parent.

An external site's CSP, frame restrictions, third-party-cookie policy, or login
flow may prevent embedding. Verified compatibility rules intercept the Baidu
homepage and cross-site knowledge pages on `ku.baidu-int.com/knowledge` before
creating an iframe. Other hosts and paths are not inferred from similar names.
The knowledge rule does not apply to same-site, same-scheme platform hosting.

For other pages, a 20-second initial-load watchdog and a burst guard (five load
events within 15 seconds) remove the iframe and show an external-opening action.
This also stops hidden frames that enter a navigation loop. A known parent CSP
violation or an available frame error signal also stops the browsing context.
During the current resource visit, the failure persists when closing/reopening
the dock; explicit refresh or a saved URL change retries it. Leaving the resource
releases its frames as described above. Other frames and task drafts remain
untouched.

Browsers may fire `load` even when a cross-origin frame fails and withhold its
`error` event and DOM. The dock therefore also offers "Page not displaying?"
to stop an otherwise undetectable blank page. This is bounded detection, not a
claim to classify every remote failure. Timeouts are not labelled as proof of a
website restriction. There is no server-side probe of arbitrary panel URLs,
credential relay, external DOM scraping or bypass of browser/site controls.
Even in the same browser profile, a login that works in a normal tab may fail
inside a cross-site frame: first-party and embedded cookie/storage contexts are
not equivalent. Team Mesh does not extract or copy browser cookies. Sites whose
authentication is incompatible with embedding should use the new-tab action.

## API and limits

Routes (all require a human user and resource authorization):

| Resource | Collection | Item |
| --- | --- | --- |
| Workspace | `/api/workspaces/{id}/web-panels` | `/api/workspaces/{id}/web-panels/{panelId}` |
| Task | `/api/issues/{id}/web-panels` | `/api/issues/{id}/web-panels/{panelId}` |

Collections support GET/POST; items support PATCH/DELETE. Writes accept `name`
and `url` only. Responses include resource/workspace identity, creator,
timestamps, and per-user `can_manage`; lists also return `can_add` and `limit`.

- Up to 50 panels per resource, with serialized resource-level count checks.
- Custom page name: optional, up to 120 Unicode characters after trimming.
  Omitted or blank names use the entire URL, without truncating its query or
  fragment. Editing an unnamed panel keeps the name optional; changing its URL
  updates the derived name. Explicit custom names are preserved.
- URL: up to 4096 UTF-8 bytes, HTTP(S), hostname required, no embedded credentials
  or raw control/space characters. Invalid URLs are never navigated.
- The exact same URL cannot be added twice to one resource. Query/fragment
  differences remain distinct. SHA-256 URL indexes support long valid URLs.
- Duplicate/limit conflicts return 409; unauthorized management returns 403.

## Implementation and verification

The clean `multica` submodule is not modified. Go handlers/router and checked
SQLc constant changes use the existing Go overlay. Shared core/views components,
API parsing, event wiring, locales and host-layout changes use the explicit
frontend stage. Web and desktop reuse the same shared layout/components.

Apply team migrations **523–528 before starting the new API**. Migration 528
allows full URL-derived names while retaining the custom-name limit; it does
not rewrite existing panels. Its down migration fails atomically if longer
URL-derived names remain: rename those panels before downgrading rather than
discarding their data. The table has no
foreign keys or cascade deletes, following repository conventions. Each explicit
index is in its own concurrent-index migration. Rollback requires reverting the
API overlay first, then applying down migrations in reverse order; dropping the
table removes only panel metadata.

Focused tests:

```sh
python3 scripts/resource-web-panels-test.py
python3 overlays/prepare.py
python3 overlays/prepare.py --check
# Use an explicitly isolated database with upstream and team migrations applied.
# Never point tests at a production database.
(cd multica/server && MULTICA_RUN_REAL_AGENT_SMOKE=0 \
  GOFLAGS="-overlay=$PWD/../../overlays/go-overlay.json -mod=readonly" \
  go test -race ./internal/handler -run '^TestResourceWebPanel' -count=1)
# In the prepared frontend stage:
pnpm --filter @multica/core exec vitest run web-panels/panel-store.test.ts api/web-panel-schema.test.ts platform/storage-cleanup.test.ts
pnpm --filter @multica/views exec vitest run web-panels layout/dashboard-layout.test.tsx
```

Tests cover scope/roles, transfer/revocation/deletion, concurrent duplicates,
limits, URL boundaries, shared UI actions, account isolation and frame lifetime.
Real-page iframe/auth behavior still depends on each external application's
embedding policy. Deployment and client-daemon restarts are not part of this
implementation step.

## Agent additions from a task conversation

An explicit user request in a task conversation can add a page through
`POST /api/agent/issue-web-panels` with only `name` and `url`. Apply migration
**527** before using this API. Its current issue is derived from the authenticated
`mat_` run token, not supplied by the caller. The run must be running and still
bound to the same agent, owner and workspace; the agent owner and originating
human must both remain workspace members. Issue transfers, completed/cancelled
runs, archived agents and expired/revoked tokens do not retain this permission.

The operation records the originating human, source agent and source run.
It cannot edit/remove an existing panel, choose another issue, or manage a
workspace panel. It returns 201 for a new item, or 200 with `already_exists`
for an identical URL, leaving the original name and ownership unchanged.
That makes retrying an uncertain result safe. Existing URL/count limits apply.
The normal workspace event updates the task's list without forcing it open.

The server adds an operational reference to the existing `multica-platform`
skill bundle and advertises it in that skill's routing text. New runs receive
the changed bundle through the existing claim/resolve mechanism. This does not
require changing/restarting the client daemon. The CLI used by the agent must
include `multica issue panel add --name NAME --url URL`; the built-in reference
teaches this command, not direct HTTP calls or a bypass using other credentials.
This implementation builds a separate local CLI for validation and does not
replace any installed client binary. It does not give the agent a human
credential or database access. Already
running turns are not retroactively patched. A standalone chat without a current
issue is intentionally not authorized to choose a task for this endpoint.

Local verification on 2026-10-18 passed the database-backed handler race tests,
core/view type checks, feature lint checks and production Web compilation. An
isolated browser/account exercised real API-backed add/edit/remove actions,
click-to-load behavior, exact query/fragment routing, frame/form retention,
keyboard/pointer resizing, new-tab navigation, task draft retention and inspector
width restoration. The external page was a local fixture, not an assertion that
all third-party login/embedding policies work. Desktop packaging and remote CI
were not run. Local compilation used a test-only installed font to avoid Google
font downloads; production font configuration was not changed.
