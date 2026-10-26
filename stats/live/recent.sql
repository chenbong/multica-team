BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '1s';
WITH bounds AS (SELECT current_timestamp AS at,current_timestamp-interval '24 hours' AS since),
transitions AS (
 SELECT l.issue_id FROM activity_log l CROSS JOIN bounds b
 LEFT JOIN issue_status t ON t.workspace_id=l.workspace_id AND t.key=l.details->>'to'
 LEFT JOIN issue_status f ON f.workspace_id=l.workspace_id AND f.key=l.details->>'from'
 WHERE l.created_at>=b.since AND l.created_at<b.at AND l.action='status_changed'
 AND coalesce(t.category,l.details->>'to')='done'
 AND coalesce(f.category,l.details->>'from') IS NOT NULL AND coalesce(f.category,l.details->>'from')<>'done'
 AND EXISTS(SELECT 1 FROM issue WHERE id=l.issue_id)
)
SELECT jsonb_build_object('observed_at',b.at,'window_start',b.since,
 'active_users',NULL,'page_views',NULL,
 'messages',(SELECT count(*) FROM chat_message m JOIN chat_session s ON s.id=m.chat_session_id WHERE m.created_at>=b.since AND m.created_at<b.at AND m.message_kind='message' AND m.role IN('user','assistant'))+
            (SELECT count(*) FROM comment c JOIN issue i ON i.id=c.issue_id WHERE c.created_at>=b.since AND c.created_at<b.at AND c.deleted_at IS NULL AND c.type='comment' AND c.author_type IN('member','agent')),
 'created_issues',(SELECT count(*) FROM issue WHERE created_at>=b.since AND created_at<b.at),
 'completed_issues',(SELECT count(DISTINCT issue_id) FROM transitions),
 'unavailable_metrics',jsonb_build_array('active_users','page_views')
) FROM bounds b;
COMMIT;
