#!/usr/bin/env python3
"""Validate and materialize the complete Go overlay manifest.

Every file under go/files is a full replacement for the same path in the
clean Multica submodule. No source file in the submodule is edited.
"""
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
    status = git("status", "--porcelain")
    if status:
        raise SystemExit("multica submodule is not clean:\n" + status)

    mapping = {}
    manifest = {}
    for override in sorted(FILES.rglob("*.go")):
        rel = override.relative_to(FILES)
        source = UPSTREAM / rel
        if not source.is_file():
            raise SystemExit(f"overlay target is absent from upstream: {rel}")
        mapping[str(source)] = str(override)
        manifest[str(rel)] = {
            "source_sha256": sha256(source),
            "overlay_sha256": sha256(override),
        }

    if not mapping:
        raise SystemExit("no Go overlay files found")
    OVERLAY_JSON.write_text(json.dumps({"Replace": mapping}, indent=2) + "\n")
    (HERE / "upstream.json").write_text(
        json.dumps({"commit": git("rev-parse", "HEAD"), "files": manifest}, indent=2)
        + "\n"
    )
    print(f"prepared {len(mapping)} Go overlay files for {git('rev-parse', '--short', 'HEAD')}")


if __name__ == "__main__":
    main()
