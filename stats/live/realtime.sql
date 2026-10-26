-- Aggregate-only production read. No message text, names, raw emails or IDs leave PostgreSQL.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '1s';
WITH bounds AS (SELECT current_timestamp AS at,current_timestamp-interval '1 hour' AS since),
engaged AS (
 SELECT q.agent_id,q.issue_id,q.chat_session_id,r.daemon_id FROM agent_task_queue q
 JOIN agent a ON a.id=q.agent_id
 LEFT JOIN agent_runtime r ON r.id=q.runtime_id
 CROSS JOIN bounds b
 WHERE q.started_at IS NOT NULL AND q.started_at<b.at
 AND q.status NOT IN ('pending','queued','dispatched')
 AND (q.completed_at IS NULL OR q.completed_at>=q.started_at)
 AND (q.started_at>=b.since OR q.completed_at>b.since
      OR (q.completed_at IS NULL AND q.status='running'
          AND (q.runtime_id IS NULL OR (r.last_seen_at>=b.since AND r.last_seen_at<b.at))))
), active_machines AS (
 SELECT r.daemon_id FROM agent_runtime r CROSS JOIN bounds b
 WHERE r.last_seen_at>=b.since AND r.last_seen_at<b.at
 UNION SELECT daemon_id FROM engaged
), completed AS (
 SELECT l.issue_id,l.created_at,i.creator_id AS user_id FROM activity_log l
 JOIN issue i ON i.id=l.issue_id AND i.creator_type='member'
 LEFT JOIN issue_status t ON t.workspace_id=l.workspace_id AND t.key=l.details->>'to'
 LEFT JOIN issue_status f ON f.workspace_id=l.workspace_id AND f.key=l.details->>'from'
 WHERE l.action='status_changed' AND l.created_at>=(SELECT at FROM bounds)-interval '24 hours'
 AND coalesce(t.category,l.details->>'to')='done'
 AND coalesce(f.category,l.details->>'from') IS NOT NULL
 AND coalesce(f.category,l.details->>'from')<>'done'
 UNION ALL
 SELECT l.issue_id,l.created_at,NULL::uuid FROM activity_log l JOIN issue i ON i.id=l.issue_id AND i.creator_type<>'member'
 LEFT JOIN issue_status t ON t.workspace_id=l.workspace_id AND t.key=l.details->>'to'
 LEFT JOIN issue_status f ON f.workspace_id=l.workspace_id AND f.key=l.details->>'from'
 WHERE l.action='status_changed' AND l.created_at>=(SELECT at FROM bounds)-interval '24 hours'
 AND coalesce(t.category,l.details->>'to')='done'
 AND coalesce(f.category,l.details->>'from') IS NOT NULL AND coalesce(f.category,l.details->>'from')<>'done'
), machines AS (
 SELECT DISTINCT ON (daemon_id) daemon_id,created_at,owner_id FROM agent_runtime
 WHERE nullif(daemon_id,'') IS NOT NULL ORDER BY daemon_id,created_at,id
), events AS (
 SELECT 'completed' AS kind,issue_id::text AS object_id,created_at AS at,user_id FROM completed
 UNION ALL SELECT 'agent_started',agent_id::text,started_at,originator_user_id FROM agent_task_queue
 WHERE started_at>=(SELECT at FROM bounds)-interval '24 hours'
 UNION ALL SELECT 'created',id::text,created_at,CASE WHEN creator_type='member' THEN creator_id ELSE NULL END FROM issue
 WHERE created_at>=(SELECT at FROM bounds)-interval '24 hours'
 UNION ALL SELECT 'machine_connected',daemon_id,created_at,owner_id FROM machines
 WHERE created_at>=(SELECT at FROM bounds)-interval '24 hours'
), buckets AS (
 SELECT kind,date_trunc('minute',at) AS at,count(DISTINCT object_id) AS amount,array_agg(DISTINCT user_id) FILTER(WHERE user_id IS NOT NULL) AS users
 FROM events WHERE at<(SELECT at FROM bounds) GROUP BY kind,date_trunc('minute',at) ORDER BY at DESC,kind LIMIT 20
), safe_accounts AS (
 SELECT u.id,split_part(lower(u.email),'@',1) || CASE WHEN split_part(lower(u.email),'@',2) ~ '^[a-z0-9-]+\.baidu\.com$' THEN '_' || split_part(split_part(lower(u.email),'@',2),'.',1) ELSE '' END AS account
 FROM "user" u WHERE u.id IN (SELECT unnest(users) FROM buckets)
), masked AS (
 SELECT id,'@' || CASE WHEN length(account)>3 THEN left(account,2)||'***'||right(account,1)
 WHEN length(account)>1 THEN left(account,1)||'***' ELSE '***' END AS label FROM safe_accounts WHERE account<>''
)
SELECT jsonb_build_object(
 'observed_at',(SELECT at FROM bounds),'window_start',(SELECT at FROM bounds)-interval '1 hour',
 'active_users',NULL,'working_agents',(SELECT count(DISTINCT agent_id) FROM engaged),
 'working_issues',(SELECT count(DISTINCT issue_id) FROM engaged),
 'working_chats',(SELECT count(DISTINCT chat_session_id) FROM engaged),
 'online_machines',(SELECT count(DISTINCT daemon_id) FROM active_machines WHERE nullif(daemon_id,'') IS NOT NULL),
 'scale',jsonb_build_object('users',(SELECT count(*) FROM "user"),'issues',(SELECT count(*) FROM issue),
  'agents',(SELECT count(*) FROM agent),'machines',(SELECT count(DISTINCT daemon_id) FROM agent_runtime WHERE nullif(daemon_id,'') IS NOT NULL)),
 'events',(SELECT coalesce(jsonb_agg(jsonb_build_object('kind',b.kind,'at',b.at,'count',b.amount,'members',(SELECT coalesce(jsonb_agg(m.label ORDER BY m.label),'[]') FROM(SELECT DISTINCT label FROM masked WHERE id=ANY(b.users) ORDER BY label LIMIT 5)m)) ORDER BY b.at DESC,b.kind),'[]') FROM buckets b),
 'unavailable_metrics',jsonb_build_array('active_users')
);
COMMIT;
