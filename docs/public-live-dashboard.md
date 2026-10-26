# Public Team Mesh dashboard

## Agreed product scope

- Public `/live` route, linked from login, with Start using and Quick start links.
- Public responses contain aggregates and event categories with redacted account
  labels only. No full names/accounts/emails, workspace/task names, source
  content, host addresses, user IDs, profile links or tokens. Redaction is not a
  guarantee of anonymity. Real usernames must be redacted on the server, never
  returned intact for browser-side masking.
- The "此刻" cards all cover the rolling last hour: distinct active humans
  (web interaction or accepted InfoFlow input), agents that participated in
  execution, distinct issues/chats processed, and machines with a heartbeat or
  execution in the window, deduplicated by daemon identity. Include completed
  work and long-running executions overlapping the hour; exclude queued work,
  future starts and old unfinished runs without a recent runtime heartbeat.
  The user label is "正在活跃的用户"; the scope annotation says
  "最近 1 小时 每 10 秒更新". These are not instantaneous online/run counts.
- Automated runs and built-in agents count without a source split. They do not
  increase human activity. All test/acceptance activity is included.
- The section titled "今天，一起推进了这些事" uses a rolling last-24-hour
  window, not midnight-to-now. Its adjacent note explicitly says "最近 24 小时".
  Count distinct active users, signed-in page views, human/agent messages,
  created issues and distinct issues that entered done in one consistent
  `[observed_at - 24 hours, observed_at)` window. Do not sum daily unique-user
  buckets to approximate it. Day-based trend snapshots remain Asia/Shanghai.
- Seven complete days, excluding today. New user/PV telemetry has no invented
  history; unavailable points are null, not zero. No incomplete-day comparisons.
  Show Beijing weekdays, numeric values, a scaled vertical axis and focus/hover
  tooltips with dates and exact values. Each numeric section retains its refresh
  frequency label; remove the separate on-page data-explanation section and
  the redacted-display badge, but preserve the conspicuous local demo warning.
- Scale: surviving user, issue, agent and daemon counts; not an assertion of an
  all-time ledger including deleted records.
- Three capability stories: multi-agent task participation, InfoFlow delegation,
  and automated wakeups, backed by available seven-day aggregates. No invented
  time savings, costs, success rates or productivity claims.
- Redacted feed: real important events, coalesce repeated categories, at most
  20 rows, preserve event times, never loop old activity as fresh activity.
  Completed issues show their initiators; new issues show their creators;
  started agent runs show the human initiating that run; machine connections
  show the enrolling user. If attribution cannot be verified, omit the name
  instead of guessing. Dedupe users by identity before masking; do not infer
  identity from generated message text. The local preview contains only already
  masked fictional labels, never real account data.
- Two refresh tiers: rolling-hour metrics, rolling-24-hour metrics, activity feed
  and current platform scale every 10 seconds; seven-day charts/capabilities
  every 300 seconds. Keep the last good
  section on failure, mark delayed and show its real last success time. Do not
  replace unavailable values with zeros or keep a fake live animation running.

## Activity and page views

New authenticated telemetry records account identity and server timestamps,
never entered text, message content, mouse paths, URLs, titles or query strings.
Actual click, keyboard/input or scroll activity is rate-limited. Background
heartbeats and visibility alone are not actual activity. Account identity comes
from authentication, not the request body. InfoFlow input joins by verified user.

Each signed-in business-page open, manual reload, navigation or back/forward
counts one page view. Scrolling, same-page panels/tabs, query/filter changes,
data refresh, background API traffic, login/errors, `/live` and anonymous visits
do not count. An event ID makes retries idempotent; separate tabs and deliberate
new visits remain separate page views. Do not persist an event's path or content.

Store daily aggregate snapshots for historical readback. Clearly version the
metric definition and identify the first partial collection day. Public reads
must be cached/coalesced and bounded, without exposing a query/SQL interface.
Recording/snapshot jobs must not execute agents or restart client daemons.

## Local source separation

The local responsive preview now supports explicitly separated demo and real
read-only modes. Real mode polls the Go API; a loopback-only development source
adapter supplies fixed, read-only PostgreSQL aggregate fixtures over SSH.
The three background jobs are serialized, bounded by database/transport timeouts,
and cached independently at 10/10/300 seconds. Visitor requests only read memory.
Private local snapshots and last-good caches survive restarts. Complete-day
snapshots are frozen, and raw usernames are redacted inside PostgreSQL before
any result leaves the production host. No production files, data, configuration
or running services are changed by this validation mode.

The existing per-day client reports cannot supply the agreed interaction-based
active users or page views. Those values stay null in real mode, never replaced
with older lower-bound metrics or demo numbers. First-day coverage must remain
explicit when collection is eventually enabled. Real mode defaults its chart to
the available message series rather than invented historical activity.

The adapter is only a loopback development fixture, never a new production
service. The updated preview routes its results through the same Go collector
and stores safe caches/snapshots in its own loopback database schema. Native
collection tests use separate disposable schemas; their fake activity is never
mixed into the real read-only preview. No production data or configuration is
written. The preview cannot show real active/PV values until production
collection is explicitly released; unavailable values remain null.

## Existing Go API implementation

`MULTICA_PUBLIC_LIVE_ENABLED=true` opts API and Web in; default is disabled.
Apply migrations 536-541 and `deploy/grant-live-stats.sql` for the existing API
database role before enabling. These are future release steps, not operations
performed by the local preview script. The Go service owns an independent pool
of at most two connections, with five-second statement and one-second lock
limits. Serial jobs run at 10/10/300 seconds with eight-second refresh deadlines.
Visitor GET requests only read memory. Errors retain/mark the last good result;
versioned PostgreSQL caches and daily frozen snapshots survive restart. Standby
does not start workers; shutdown cancels jobs and closes the owned pool.

The authenticated dashboard guard mounts the Web reporter. Its only body fields
are `{event_id,page_view}` at `POST /api/live/activity`; authentication derives
the account and the server derives the timestamp. Resource visits wait for
successful resource loading. Trusted visible input events are throttled to one
activity report/minute; no polling or visibility heartbeat counts. An event UUID
deduplicates retries, while tabs and new visits get separate IDs. Account changes
cancel unsent retries. The endpoint rejects machine/task actors, unknown fields,
supplied identities/timestamps/URLs, bodies over 1 KiB and more than 120 reporting
attempts/account/minute. No contents, paths or titles are stored.

Ordinary InfoFlow chats report actual senders only after submit acceptance via
a three-second best-effort trusted endpoint, using existing identity/membership
checks. Never attribute chat activity to the robot owner's CLI identity. Existing
verified task source records contribute task-mode human activity. Reporting
failures cannot delay or fail delivery. Raw telemetry and aggregate snapshots
are retained long-term. Coverage starts at actual collector activation:
windows overlapping collection show the observed cumulative counts, explicitly
marked with `partial_metrics` ("采集中"). A date entirely before collection
remains null, never an invented zero. Counts start at the later of the window
boundary and the real collection start. Complete windows automatically clear
the marker. Rolling 24-hour message/task counts remain full business counts.

Focused tests cover retry/account/tab behavior, failed resources, forged actors,
native SQL rolling/Beijing-day boundaries, partial coverage, redaction, rate limits,
serial jobs, immutable snapshots, restart restoration and last-good recovery.
Go DB tests require the explicit managed loopback `DATABASE_URL`. The preparation
script `scripts/prepare-public-live-preview.py` refreshes only local dashboard
wiring and generates the reserved `/live` slug from the overlay JSON. Compile
Next in production mode before starting the local preview; never use `next dev`.

## Validation completed locally

- 25 focused Web tests including the actual loopback Go response contracts and
  a regression for anonymous auth cleanup cancelling public statistics queries.
- Eight focused Go tests (service, authenticated handler and standby lifecycle),
  with the race detector; native SQL uses unique disposable local schemas.
- 74 focused Bridge regressions and seven read-only source/cache tests.
- Go vet, focused Web lint, production Web build and TypeScript passed. The
  pre-existing login effect dependency lint warning is outside the new files.
- Compiled and hydrated login branding acceptance both passed. Browser readback
  confirmed immediate real data, scrolling, refresh annotations and new-tab links.
- The public route has its own Query client: anonymous auth cleanup can erase
  private session data without erasing/cancelling the public aggregate cache.
- No deployment, remote writes, Git push, or production/client service restart.

The local runner is `node scripts/run-public-live-preview.cjs`. It expects the
existing protected source/build manifests, managed loopback test database and
compiled `build/live-statistics-preview` harness. Rebuild that harness from
`multica/server` with the generated Go overlay using `go test -c` against
`./internal/service`; it is explicitly gated by the runner's local-only test env.

## Rolling-hour and animated-feed revision

The realtime cache uses definition version 3; recent/history caches use version
2. Historical snapshots use version 2 while preserving the version 1 archive:
frozen business counts are retained, only active users/PV and the per-day
`partial` flag are recovered from telemetry. Version 2 snapshots remain frozen.
Current scale now comes from the ten-second
realtime payload; `history.scale` is retained only for older response consumers.
The feed retains its existing 24-hour history window, while its refresh changes
to ten seconds. New entries fade/slide in, surviving entries move smoothly and
changed minute buckets briefly highlight. First load and unchanged polls do not
replay animations. A scrolled reader remains anchored; reduced-motion settings
disable movement and cancel ongoing animations. Demo-only controls cannot
insert real activity. Numeric change animations remain enabled.

The explicit `TestPublicLiveReadOnlyBenchmark` opt-in measures native SELECT
plans through the protected development connection, using READ ONLY,
EXPLAIN ANALYZE, timeouts and ROLLBACK. It prints only timings/buffer counts.
An optional benchmark-only guard lets it measure active/PV subqueries before
full collection coverage without changing any stored metadata or public data.
