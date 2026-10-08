#!/usr/bin/env python3
"""Stage three-way frontend ports; export reviewed deltas as exact anchors.

This is an upgrade aid only. Production builds use overlays/web-patch.py.
No command in this script writes to the upstream submodule.
"""
import argparse
import difflib
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OLD = "9fab6da917953c6f1a967a17beef045aff3d0ce5"
NEW = "2ea01ae4ef55de4310b99af192d2dbd367832883"


def upstream(ref, rel):
    return subprocess.run(
        ["git", "-C", str(ROOT / "multica"), "show", f"{ref}:{rel}"],
        capture_output=True,
        text=True,
    )


def prepare(stage):
    original = stage / "original-overlays/web-direct"
    if not (original / "packages/core/api/client.ts").is_file():
        raise SystemExit("This one-time port aid requires the pre-upgrade overlay snapshot.")
    candidates = stage / "candidates"
    bases = stage / "bases"
    current = stage / "upstream"
    report = []
    for path in sorted(original.rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(original).as_posix()
        old, new = upstream(OLD, rel), upstream(NEW, rel)
        if new.returncode:
            report.append({"path": rel, "kind": "addition"})
            continue
        target = candidates / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        for tree, content in [(bases, old.stdout), (current, new.stdout)]:
            (tree / rel).parent.mkdir(parents=True, exist_ok=True)
            (tree / rel).write_text(content)
        merge = subprocess.run(
            ["git", "merge-file", "-p", str(path), str(bases / rel), str(current / rel)],
            capture_output=True,
            text=True,
        )
        if merge.returncode < 0 or merge.returncode > 127:
            raise RuntimeError(merge.stderr)
        target.write_text(merge.stdout)
        report.append({"path": rel, "kind": "conflict" if merge.returncode else "merged"})
    (stage / "port-inventory.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


def export(stage):
    changes = {}
    removed = []
    for path in sorted((stage / "candidates").rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(stage / "candidates").as_posix()
        before = upstream(NEW, rel).stdout
        after = path.read_text()
        if any(line.startswith(("<<<<<<<", "=======", ">>>>>>>")) for line in after.splitlines()):
            raise RuntimeError(f"Unresolved conflict: {rel}")
        a, b = before.splitlines(keepends=True), after.splitlines(keepends=True)
        matcher = difflib.SequenceMatcher(None, a, b, autojunk=False)
        for context in (3, 6, 12, 24, 48):
            replacements = []
            for group in matcher.get_grouped_opcodes(context):
                old = "".join(a[group[0][1]:group[-1][2]])
                new = "".join(b[group[0][3]:group[-1][4]])
                replacements.append((old, new))
            if all(before.count(old) == 1 for old, _new in replacements):
                break
        else:
            raise RuntimeError(f"Non-unique anchor: {rel}")
        if replacements:
            changes[rel] = replacements
        if (ROOT / "overlays/web-direct" / rel).exists():
            removed.append(rel)
    block = ["# Reviewed v0.6.1 deltas from the former full-file overlays.", "# Keep new upstream logic; every anchor is checked before any files are written.", "PORTED_CHANGES = {"]
    for rel, replacements in changes.items():
        block.append(f"    {rel!r}: [")
        for before, after in replacements:
            block.append(f"        ({before!r}, {after!r}),")
        block.append("    ],")
    block.extend(["}", "", "for _rel, _replacements in PORTED_CHANGES.items():", "    CHANGES[_rel] = _replacements + CHANGES.get(_rel, [])", ""])
    marker = "def apply_patches():"
    script = (ROOT / "overlays/web-patch.py").read_text()
    start = script.find("# Reviewed v0.6.1 deltas from the former full-file overlays.")
    end = script.index(marker)
    updated = script[:start if start >= 0 else end] + "\n".join(block) + "\n" + script[end:]
    patch = ["*** Begin Patch"]
    if script != updated:
        patch.append(f"*** Update File: {ROOT}/overlays/web-patch.py")
        for line in list(difflib.unified_diff(script.splitlines(), updated.splitlines(), n=3))[2:]:
            patch.append("@@" if line.startswith("@@") else line)
    for rel in removed:
        patch.append(f"*** Delete File: {ROOT}/overlays/web-direct/{rel}")
    patch.append("*** End Patch")
    print("\n".join(patch))


parser = argparse.ArgumentParser()
parser.add_argument("action", choices=["prepare", "export"])
parser.add_argument("stage", type=Path)
args = parser.parse_args()
stage = args.stage.resolve()
if stage == ROOT or ROOT in stage.parents or not (stage / "source/package.json").exists():
    raise SystemExit("Use the separate directory created by v061-web-stage.sh")
if args.action == "prepare":
    prepare(stage)
else:
    export(stage)
