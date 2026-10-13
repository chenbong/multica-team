"""Guard task writes against stale callers after an issue changes workspace.

Patch SQLc's checked-in query constants without changing parameters or models.
The identical edits can be applied to query sources for a SQLc regeneration.
FOR SHARE rechecks the issue row after a concurrent move and holds the row
through the INSERT; an ordinary snapshot-only EXISTS would race with transfer.
"""


def rewrite(source, name):
    if name == "subscriber":
        before = "INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason)\nVALUES ($1, $2, $3, $4)"
        after = """INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason)
SELECT $1, $2, $3, $4
FROM (SELECT workspace_id FROM issue WHERE id=$1 FOR SHARE) transfer_owner
WHERE ($2='member' AND EXISTS(SELECT 1 FROM member WHERE user_id=$3 AND workspace_id=transfer_owner.workspace_id))
   OR ($2='agent' AND EXISTS(SELECT 1 FROM agent WHERE id=$3 AND workspace_id=transfer_owner.workspace_id))"""
        if source.count(before) != 2:
            raise SystemExit("subscriber transfer guard anchors changed")
        return source.replace(before, after)
    edits = [(
        "WHERE lock_task_owner_rows($1, $3, $2)",
        "WHERE lock_task_owner_rows($1, $3, $2)\n"
        "  AND ($3::uuid IS NULL OR\n"
        "       (SELECT workspace_id FROM issue WHERE id=$3 FOR SHARE) =\n"
        "       (SELECT workspace_id FROM agent WHERE id=$1))",
        2 if name == "agent" else 1,
    )]
    if name == "agent":
        edits += [(
            "  AND lock_task_owner_rows(p.agent_id, p.issue_id, p.runtime_id)",
            "  AND lock_task_owner_rows(p.agent_id, p.issue_id, p.runtime_id)\n"
            "  AND (p.issue_id IS NULL OR\n"
            "       (SELECT workspace_id FROM issue WHERE id=p.issue_id FOR SHARE) =\n"
            "       (SELECT workspace_id FROM agent WHERE id=p.agent_id))", 2,
        ), (
            "  AND lock_task_owner_rows(NULL, $2, NULL)",
            "  AND lock_task_owner_rows(NULL, $2, NULL)\n"
            "  AND (SELECT workspace_id FROM issue WHERE id=$2 FOR SHARE) =\n"
            "      (SELECT a.workspace_id FROM agent a JOIN agent_task_queue t ON t.agent_id=a.id WHERE t.id=$1)", 1,
        )]
    for before, after, count in edits:
        if source.count(before) != count:
            raise SystemExit(f"task transfer guard anchor changed in {name}; expected {count}")
        source = source.replace(before, after)
    return source


def generated(upstream, files):
    for name in ("agent", "wakeup", "subscriber"):
        relative = f"server/pkg/db/generated/{name}.sql.go"
        # Validate query sources too so a future SQLc regeneration cannot
        # accidentally target a different set of statements.
        rewrite((upstream / f"server/pkg/db/queries/{name}.sql").read_text(), name)
        source = files / relative
        if not source.is_file():
            source = upstream / relative
        yield relative, rewrite(source.read_text(), name)
