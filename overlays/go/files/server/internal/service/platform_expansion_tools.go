package service

// Delivered through existing skill bundles; no installed CLI change is needed.
const platformExpansionTools = `#!/usr/bin/env python3
"""Task-scoped Team Mesh tools. No personal/daemon credential fallback."""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request


def request(method, path, body=None):
    token = os.environ.get("MULTICA_TOKEN", "")
    base = os.environ.get("MULTICA_SERVER_URL", "").rstrip("/")
    parsed = urllib.parse.urlsplit(base)
    if not token.startswith("mat_") or parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.query or parsed.fragment:
        raise ValueError("A current Team Mesh run environment is required")
    data = None if body is None else json.dumps(body).encode()
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json",
               "X-Workspace-ID": os.environ.get("MULTICA_WORKSPACE_ID", "")}
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    with urllib.request.build_opener(NoRedirect).open(req, timeout=30) as response:
        raw = response.read(256 * 1024 + 1)
        if len(raw) > 256 * 1024:
            raise ValueError("Response exceeds tool limit")
        return json.loads(raw) if raw else None


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="area", required=True)
    panels = sub.add_parser("panels")
    panels.add_argument("action", choices=["list", "add", "update", "delete"])
    panels.add_argument("--id")
    panels.add_argument("--name")
    panels.add_argument("--url")
    secrets = sub.add_parser("secrets")
    secrets.add_argument("action", choices=["list", "read", "with-file"])
    secrets.add_argument("--id")
    args, command = p.parse_known_args()
    if command and not (args.area == "secrets" and args.action == "with-file"):
        p.error("Unexpected command arguments")
    if args.area == "panels":
        path = "/api/agent/issue-web-panels"
        if args.action in ("update", "delete"):
            if not args.id:
                p.error("--id is required")
            path += "/" + urllib.parse.quote(args.id, safe="")
        body = {key: value for key, value in {"name": args.name, "url": args.url}.items() if value is not None}
        method = {"list": "GET", "add": "POST", "update": "PATCH", "delete": "DELETE"}[args.action]
        result = request(method, path, body if method in ("POST", "PATCH") else None)
    else:
        path = "/api/agent/personal-secrets"
        if args.action != "list":
            if not args.id:
                p.error("--id is required")
            path += "/" + urllib.parse.quote(args.id, safe="")
        result = request("GET", path)
        if args.action == "with-file":
            if command and command[0] == "--":
                command = command[1:]
            if not command or not any("{secret_file}" in item for item in command):
                p.error("A command containing {secret_file} is required")
            for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
                signal.signal(sig, lambda *_: sys.exit(128))
            with tempfile.TemporaryDirectory(prefix="team-mesh-secret-", dir=os.environ.get("TMPDIR")) as directory:
                os.chmod(directory, 0o700)
                path = Path(directory) / "value"
                fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, "w") as file:
                    file.write(result["value"])
                code = subprocess.run([item.replace("{secret_file}", str(path)) for item in command], check=False).returncode
                return code
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except urllib.error.HTTPError as error:
        print("Team Mesh request rejected: HTTP " + str(error.code), file=sys.stderr)
        sys.exit(1)
    except Exception:
        print("Team Mesh tool failed; check the live run and request parameters.", file=sys.stderr)
        sys.exit(1)
`

const personalSecretsSkillReference = `# Personal secrets owned by your agent owner

Use scripts/team-mesh-tools.py with the authenticated current run environment.
The server derives your owner from the live agent and run; you cannot select
another owner. This works in issues and chats, including shared-agent runs.

    python3 scripts/team-mesh-tools.py secrets list
    python3 scripts/team-mesh-tools.py secrets read --id SECRET_UUID
    python3 scripts/team-mesh-tools.py secrets with-file --id SECRET_UUID -- ssh -i "{secret_file}" HOST COMMAND

List returns names/descriptions only. Read returns the current secret value.
Use with-file for SSH keys: it writes a mode-0600 temporary file in a private
directory under the run TMPDIR, runs the command and cleans up on normal exit,
failure or catchable termination. Abrupt machine failure/SIGKILL cannot run
cleanup; the daemon's per-run temporary directory cleanup remains the fallback.

Never echo secret values into replies, comments, attachments, logs or source.
Never save them in the repository, reuse them across runs or send them to
unrelated services. Read only when needed for the user's requested operation.
Do not obtain a user PAT, daemon token or another run's token. A completed,
cancelled or revoked run cannot read. Deletion blocks future reads, not values
already in memory. Do not automatically retry with old cached values.
`
