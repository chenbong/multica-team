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
