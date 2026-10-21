# Configured Web release acceptance

For a configured deployment, prepare the isolated source with
`deploy/prepare-web.py --config <protected-manifest>` before building. Never
package an unconfigured local preview as a production release. Preserve the
existing font assets and build in production mode.

After building, `deploy/verify-web-release.py <generated-tree> --config <manifest>`
checks both the prepared source and the actual compiled login content. The
configured `deploy/build.sh` path runs this gate automatically. A missing title,
description, email placeholder, verification robot, robot link, quick-start link,
or community link blocks release. HTTP 200 alone is not acceptance.

Before switching Web, start the completed candidate on a separate loopback port.
Inspect the hydrated login page in a browser without signing out an existing
session. Save a public DOM snapshot with `url`, visible `text`, link `links`,
input `placeholders`, and boolean `emailLabel`/`continueButton` accessibility
checks. Pass it as `--dom <snapshot.json>` to the same verification command.
Verify layout on desktop and a narrow viewport, and check the verification-code
instructions without requesting a real code. Do not export cookies or storage.

Back up the current compiled Web and its source/configuration before replacing
the Web pointer. Record API, Bridge and database container start times and
client-program hashes; a Web-only release must leave them unchanged. Keep the
previous Web pointer and roll back only Web if acceptance fails. Repeat browser
acceptance against the live URL and fetch its referenced scripts/styles to
check for missing assets. Retain the compiled binding-card regression tests when
that feature is already deployed. Public Git files must not contain the private
manifest or deployment-specific URLs.
