BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '8s';
SET LOCAL lock_timeout = '1s';
WITH bounds AS (SELECT current_timestamp AS at,(current_timestamp AT TIME ZONE 'Asia/Shanghai')::date AS today),
days AS (SELECT (today-n)::date AS day,(today-n)::timestamp AT TIME ZONE 'Asia/Shanghai' AS a,(today-n+1)::timestamp AT TIME ZONE 'Asia/Shanghai' AS b FROM bounds CROSS JOIN generate_series(1,7)n),
transitions AS (
 SELECT DISTINCT d.day,l.issue_id FROM days d JOIN activity_log l ON l.created_at>=d.a AND l.created_at<d.b AND l.action='status_changed'
 LEFT JOIN issue_status t ON t.workspace_id=l.workspace_id AND t.key=l.details->>'to'
 LEFT JOIN issue_status f ON f.workspace_id=l.workspace_id AND f.key=l.details->>'from'
 WHERE coalesce(t.category,l.details->>'to')='done'
 AND coalesce(f.category,l.details->>'from') IS NOT NULL AND coalesce(f.category,l.details->>'from')<>'done'
 AND EXISTS(SELECT 1 FROM issue WHERE id=l.issue_id)
), participating AS (
 SELECT q.issue_id,count(DISTINCT q.agent_id) AS agents FROM agent_task_queue q JOIN agent a ON a.id=q.agent_id
 WHERE q.issue_id IS NOT NULL AND q.started_at<(SELECT max(b) FROM days)
 AND coalesce(q.completed_at,(SELECT at FROM bounds))>(SELECT min(a) FROM days)
 AND q.started_at IS NOT NULL AND EXISTS(SELECT 1 FROM issue WHERE id=q.issue_id)
 GROUP BY q.issue_id
)
SELECT jsonb_build_object('observed_at',b.at,
 'days',(SELECT jsonb_agg(jsonb_build_object('date',d.day,'active_users',NULL,'page_views',NULL,
  'messages',(SELECT count(*) FROM chat_message m JOIN chat_session s ON s.id=m.chat_session_id WHERE m.created_at>=d.a AND m.created_at<d.b AND m.message_kind='message' AND m.role IN('user','assistant'))+
             (SELECT count(*) FROM comment c JOIN issue i ON i.id=c.issue_id WHERE c.created_at>=d.a AND c.created_at<d.b AND c.deleted_at IS NULL AND c.type='comment' AND c.author_type IN('member','agent')),
  'created_issues',(SELECT count(*) FROM issue WHERE created_at>=d.a AND created_at<d.b),
  'completed_issues',(SELECT count(*) FROM transitions t WHERE t.day=d.day)) ORDER BY d.day) FROM days d),
 'scale',jsonb_build_object('users',(SELECT count(*) FROM "user"),'issues',(SELECT count(*) FROM issue),
  'agents',(SELECT count(*) FROM agent),'machines',(SELECT count(DISTINCT daemon_id) FROM agent_runtime WHERE nullif(daemon_id,'') IS NOT NULL)),
 'capabilities',jsonb_build_object('multi_agent_issues',(SELECT count(*) FROM participating WHERE agents>=2),
  'infoflow_inputs',(SELECT count(DISTINCT comment_id) FROM infoflow_issue_source WHERE comment_id IS NOT NULL AND created_at>=(SELECT min(a) FROM days) AND created_at<(SELECT max(b) FROM days)),
  'wakeup_triggers',(SELECT count(*) FROM activity_log WHERE action='wakeup_triggered' AND created_at>=(SELECT min(a) FROM days) AND created_at<(SELECT max(b) FROM days))),
 'unavailable_metrics',jsonb_build_array('active_users','page_views')
) FROM bounds b;
COMMIT;
