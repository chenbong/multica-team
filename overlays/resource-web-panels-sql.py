"""Compose resource-owned panel cleanup with the checked SQLc overlays."""
import re


def rewrite(source, kind):
    name = {"issue": "DeleteIssue", "workspace": "DeleteWorkspace", "workspace_issue_roots": "DeleteWorkspaceIssueRoots"}[kind]
    pattern = rf"(?ms)(-- name: {name} :exec\n)(.*?)(?=^-- name:|^`|\Z)"
    matches = list(re.finditer(pattern, source))
    if len(matches) != 1:
        raise SystemExit(f"web panel cleanup query changed: {name}")
    match = matches[0]
    query = match.group()
    if kind == "issue":
        before = "cleared_wakeup_receipts AS (\n"
        cleanup = "DELETE FROM resource_web_panel WHERE issue_id IN (SELECT target.id FROM target)"
    elif kind == "workspace":
        before = "cleared_client_usage_workspace AS (\n"
        cleanup = "DELETE FROM resource_web_panel WHERE workspace_id = $1 OR issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)"
    else:
        before = "WITH deleted_wakeup_receipts AS (\n"
        # The HTTP teardown removes issue roots before the final workspace
        # sweep. Use the SAME statement snapshot as root deletion, while the
        # issue-to-workspace relationship is still visible. CTE execution order
        # is deliberately not relied on; the enclosing teardown holds locks.
        cleanup = "DELETE FROM resource_web_panel WHERE workspace_id = $1 OR issue_id IN (SELECT id FROM issue WHERE workspace_id = $1)"
    if query.count(before) != 1:
        raise SystemExit(f"web panel cleanup anchor changed: {name}")
    if kind == "workspace_issue_roots":
        after = f"WITH cleared_resource_web_panels AS (\n    {cleanup}\n), deleted_wakeup_receipts AS (\n"
    else:
        after = f"cleared_resource_web_panels AS (\n    {cleanup}\n),\n{before}"
    query = query.replace(before, after, 1)
    return source[:match.start()] + query + source[match.end():]


def extend(upstream, files, generated):
    result = list(generated)
    for kind, filename in (("issue", "issue"), ("workspace", "workspace"), ("workspace_issue_roots", "workspace_delete")):
        rewrite((upstream / f"server/pkg/db/queries/{filename}.sql").read_text(), kind)
        relative = f"server/pkg/db/generated/{filename}.sql.go"
        hits = [i for i, (rel, _) in enumerate(result) if rel == relative]
        if len(hits) > 1:
            raise SystemExit(f"duplicate web panel SQL overlay: {relative}")
        if hits:
            i = hits[0]
            result[i] = (relative, rewrite(result[i][1], kind))
        else:
            source = files / relative
            if not source.is_file():
                source = upstream / relative
            result.append((relative, rewrite(source.read_text(), kind)))
    return result
