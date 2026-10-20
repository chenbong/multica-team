"""Snapshot explicit wakeup notification instructions when a run is created."""


def generated(upstream, files):
    rel = "server/internal/service/issue_wakeup.go"
    source = files / rel
    if not source.exists():
        source = upstream / rel
    text = source.read_text()
    before = '"wakeup_revision": w.Revision, "wakeup_evidence": evidence'
    if text.count(before) != 1:
        raise SystemExit("wakeup notification snapshot anchor changed")
    yield rel, text.replace(before, '"wakeup_revision": w.Revision, "wakeup_instruction": w.Instruction, "wakeup_evidence": evidence', 1)
