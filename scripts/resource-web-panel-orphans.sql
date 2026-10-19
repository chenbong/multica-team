-- Audit only: no names, URLs, credentials, or data mutations are emitted.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;

SELECT panel.id,
       CASE WHEN panel.workspace_id IS NOT NULL THEN 'workspace' ELSE 'issue' END AS scope,
       COALESCE(panel.workspace_id, panel.issue_id) AS missing_resource_id,
       CASE WHEN panel.workspace_id IS NOT NULL THEN 'missing_workspace' ELSE 'missing_issue' END AS reason,
       panel.created_at
FROM resource_web_panel AS panel
LEFT JOIN workspace ON workspace.id = panel.workspace_id
LEFT JOIN issue ON issue.id = panel.issue_id
WHERE (panel.workspace_id IS NOT NULL AND workspace.id IS NULL)
   OR (panel.issue_id IS NOT NULL AND issue.id IS NULL)
ORDER BY panel.created_at, panel.id;

ROLLBACK;
