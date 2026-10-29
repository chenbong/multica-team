#!/usr/bin/env python3
"""Validate and materialize the complete Go overlay manifest.

Every file under go/files is a full replacement for the same path in the
clean Multica submodule. No source file in the submodule is edited.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import runpy

HERE = Path(__file__).resolve().parent
TEAM = HERE.parent
UPSTREAM = TEAM / "multica"
FILES = HERE / "go" / "files"
OVERLAY_JSON = HERE / "go-overlay.json"


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(*args: str) -> str:
    return subprocess.check_output(["git", "-C", str(UPSTREAM), *args], text=True).strip()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--output", type=Path, default=OVERLAY_JSON,
        help="Go overlay JSON destination (all replacement paths are physical paths)",
    )
    parser.add_argument(
        "--check", action="store_true",
        help="verify the recorded upstream commit and hashes without writing files",
    )
    args = parser.parse_args()
    status = git("status", "--porcelain")
    if status:
        raise SystemExit("multica submodule is not clean:\n" + status)

    mapping = {}
    manifest = {}
    for override in sorted(FILES.rglob("*.go")):
        rel = override.relative_to(FILES)
        source = UPSTREAM / rel
        if not source.is_file() and not source.parent.is_dir():
            raise SystemExit(f"overlay target is absent from upstream: {rel}")
        if any(line.startswith(("<<<<<<< ", "||||||| ", ">>>>>>> "))
               for line in override.read_text().splitlines()):
            raise SystemExit(f"unresolved merge conflict in overlay: {rel}")
        mapping[str(source)] = str(override)
        manifest[str(rel)] = {
            "source_sha256": sha256(source) if source.is_file() else None,
            "overlay_sha256": sha256(override),
        }

    # Small checked SQL-constant edits avoid vendoring thousands of unchanged
    # SQLc lines. They retain the same non-invasive build overlay boundary.
    guards = runpy.run_path(str(HERE / "issue-transfer-sql.py"))
    chat = runpy.run_path(str(HERE / "chat-access-go-patch.py"))
    generated = list(guards["generated"](UPSTREAM, FILES)) + list(chat["generated"](UPSTREAM, FILES))
    panel_agent = runpy.run_path(str(HERE / "resource-web-panels-agent.py"))
    generated += panel_agent["generated"](UPSTREAM, FILES)
    daemon_updates = runpy.run_path(str(HERE / "daemon-auto-update.py"))
    generated += list(daemon_updates["generated"](UPSTREAM, FILES))
    wakeup_snapshot = runpy.run_path(str(HERE / "infoflow-wakeup-snapshot.py"))
    generated += list(wakeup_snapshot["generated"](UPSTREAM, FILES))
    builtin_env = runpy.run_path(str(HERE / "builtin-agent-env-sql.py"))
    generated = builtin_env["extend"](UPSTREAM, generated)
    web_panels = runpy.run_path(str(HERE / "resource-web-panels-sql.py"))
    generated = web_panels["extend"](UPSTREAM, FILES, generated)
    public_live = runpy.run_path(str(HERE / "public-live-go.py"))
    generated = public_live["extend"](UPSTREAM, FILES, generated)
    ws_origin = runpy.run_path(str(HERE / "websocket-origin-go.py"))
    generated = ws_origin["extend"](UPSTREAM, FILES, generated)
    http_origin = runpy.run_path(str(HERE / "http-origin-go.py"))
    generated = http_origin["extend"](UPSTREAM, FILES, generated)
    six_features = runpy.run_path(str(HERE / "team-features-go.py"))
    generated = six_features["extend"](UPSTREAM, FILES, generated)
    project_directory = runpy.run_path(str(HERE / "project-working-directory.py"))
    generated = project_directory["extend"](UPSTREAM, FILES, generated)
    skill_governance = runpy.run_path(str(HERE / "skill-governance-go.py"))
    generated = skill_governance["extend"](UPSTREAM, FILES, generated)
    for rel, text in generated:
        source = UPSTREAM / rel
        override = HERE / ".generated" / rel
        mapping[str(source)] = str(override)
        manifest[rel] = {"source_sha256": sha256(source) if source.is_file() else None, "overlay_sha256": hashlib.sha256(text.encode()).hexdigest()}
        if not args.check:
            override.parent.mkdir(parents=True, exist_ok=True)
            override.write_text(text)
        elif not override.is_file() or override.read_text() != text:
            raise SystemExit(f"generated SQL overlay missing or stale: {rel}; rerun prepare.py")

    if not mapping:
        raise SystemExit("no Go overlay files found")
    current = {"commit": git("rev-parse", "HEAD"), "files": manifest}
    manifest_path = HERE / "upstream.json"
    if args.check:
        if not manifest_path.is_file() or json.loads(manifest_path.read_text()) != current:
            raise SystemExit("upstream commit or Go overlay hashes differ from upstream.json")
        print(f"verified {len(mapping)} Go overlay files for {current['commit']}")
        return
    args.output.resolve().write_text(json.dumps({"Replace": mapping}, indent=2) + "\n")
    manifest_path.write_text(json.dumps(current, indent=2) + "\n")
    print(f"prepared {len(mapping)} Go overlay files for {git('rev-parse', '--short', 'HEAD')}")


if __name__ == "__main__":
    main()
