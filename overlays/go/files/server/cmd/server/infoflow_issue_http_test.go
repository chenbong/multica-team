package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Exercise the actual router and service-token boundary, not direct handler calls.
// This test never runs migrations, starts a daemon, or invokes an agent process.
func TestInfoFlowIssueAuthenticatedHTTP(t *testing.T) {
	if os.Getenv("MULTICA_INFOFLOW_HTTP_TEST") != "1" {
		t.Skip("opt-in isolated InfoFlow HTTP test")
	}
	dsn := os.Getenv("DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() != "127.0.0.1" || u.Port() != "55439" || u.Path != "/infoflow_binding_test" {
		t.Fatal("requires dedicated 127.0.0.1:55439/infoflow_binding_test database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Validate the parsed destination too: URL query parameters can override it.
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "infoflow_binding_test" {
		t.Fatal("database connection overrides must stay on the isolated fixture")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 55439 {
			t.Fatal("database fallbacks must stay on the isolated fixture")
		}
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var database string
	var schemaReady bool
	if err := pool.QueryRow(context.Background(), `SELECT current_database(), to_regclass('public.infoflow_issue_binding') IS NOT NULL AND to_regclass('public.infoflow_issue_source') IS NOT NULL AND to_regclass('public.infoflow_issue_delivery') IS NOT NULL`).Scan(&database, &schemaReady); err != nil {
		t.Fatal(err)
	}
	if database != "infoflow_binding_test" || !schemaReady {
		t.Fatal("isolated database must already have the InfoFlow migrations applied")
	}

	// Avoid inheriting live integrations when constructing the production router.
	t.Setenv("MULTICA_STANDBY", "true")
	for _, key := range []string{"MULTICA_CLOUD_URL", "MULTICA_LLM_API_KEY", "COMPOSIO_API_KEY", "S3_BUCKET", "RESEND_API_KEY", "SMTP_HOST"} {
		t.Setenv(key, "")
	}
	t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
	t.Setenv("JWT_SECRET", "isolated-infoflow-http-test-jwt-secret")
	t.Setenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN", "example.test")
	serviceToken := "infoflow-test-" + uuid.NewString()
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", serviceToken)

	f := testutil.New(pool, "", "")
	suffix := uuid.NewString()
	ownerName := "InfoFlow fixture workspace owner"
	owner := f.User(t, ownerName, "owner-"+suffix+"@example.test")
	actorSender := "actor-" + suffix
	actor := f.User(t, "InfoFlow incoming actor", actorSender+"@example.test")
	outsiderSender := "outsider-" + suffix
	f.User(t, "InfoFlow outsider", outsiderSender+"@example.test")
	workspace := f.Workspace(t, "InfoFlow HTTP fixture", "if-http-"+suffix, testutil.Cols{"issue_prefix": "IFHTTP"})
	f.Member(t, workspace, owner, "owner")
	// An admin can manage bindings but must not invoke somebody else's private agent.
	actorMembership := f.Member(t, workspace, actor, "admin")
	f.WorkspaceID, f.UserID = workspace, owner
	runtime := f.Runtime(t, "Offline HTTP fixture runtime", testutil.Cols{"status": "offline"})
	agent := f.Agent(t, "Private HTTP fixture agent", runtime)
	issue := f.Issue(t, "InfoFlow real-router authorization fixture", testutil.Cols{"assignee_type": "agent", "assignee_id": agent})
	robot := "http-robot-" + suffix

	// Only remove rows belonging to this test's random scope. Fixture builders
	// clean up their own rows after these HTTP-created dependents are removed.
	f.Cleanup(t, `DELETE FROM infoflow_issue_binding WHERE robot_id=$1 AND workspace_id=$2`, robot, workspace)
	f.Cleanup(t, `DELETE FROM infoflow_issue_source WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1 AND workspace_id=$2)`, robot, workspace)
	f.Cleanup(t, `DELETE FROM infoflow_issue_delivery WHERE binding_id IN (SELECT id FROM infoflow_issue_binding WHERE robot_id=$1 AND workspace_id=$2)`, robot, workspace)
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, issue)
	f.Cleanup(t, `DELETE FROM comment WHERE issue_id=$1`, issue)
	f.Cleanup(t, `DELETE FROM activity_log WHERE issue_id=$1`, issue)
	f.Cleanup(t, `DELETE FROM inbox_item WHERE issue_id=$1`, issue)
	f.Cleanup(t, `DELETE FROM issue_subscriber WHERE issue_id=$1`, issue)

	pat := "mul_" + uuid.NewString()
	patHash := sha256.Sum256([]byte(pat))
	f.Insert(t, "personal_access_token", testutil.Cols{"user_id": owner, "name": "isolated-infoflow-http-test", "token_hash": hex.EncodeToString(patHash[:]), "token_prefix": pat[:12]})
	hub := realtime.NewHub()
	go hub.Run()
	server := httptest.NewServer(NewRouter(pool, hub, events.New(), analytics.NoopClient{}, nil))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 15 * time.Second
	call := func(t *testing.T, method, path string, body any, headers map[string]string, want int) []byte {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, data)
		}
		return data
	}
	type relayResponse struct {
		Handled    bool   `json:"handled"`
		Silent     bool   `json:"silent"`
		CanComment bool   `json:"can_comment"`
		Notice     string `json:"notice"`
		CommentID  string `json:"comment_id"`
		Binding    *struct {
			BindingID string `json:"binding_id"`
			Group     *struct {
				State         string `json:"state"`
				LeaderAgentID string `json:"leader_agent_id"`
			} `json:"group"`
			IssueID       string `json:"issue_id"`
			Identifier    string `json:"identifier"`
			WorkspaceSlug string `json:"workspace_slug"`
			Generation    string `json:"generation"`
		} `json:"binding"`
	}
	decode := func(t *testing.T, data []byte) relayResponse {
		t.Helper()
		var result relayResponse
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	const relayPath = "/api/integrations/infoflow/issue-relay"
	scope := map[string]any{"robot_id": robot, "agent_id": agent, "workspace_id": workspace}
	trustedHeaders := map[string]string{"Authorization": "Bearer " + serviceToken}
	paths := []string{relayPath, "/api/integrations/infoflow/issue-groups", "/api/integrations/infoflow/issue-group-status", "/api/integrations/infoflow/issue-deliveries", "/api/integrations/infoflow/issue-deliveries/" + uuid.NewString() + "/ack"}

	t.Run("ordinary_PAT_is_valid_but_never_a_relay_credential", func(t *testing.T) {
		call(t, http.MethodGet, "/api/me", nil, map[string]string{"Authorization": "Bearer " + pat}, http.StatusOK)
	})
	t.Run("all_routes_reject_non_service_identity", func(t *testing.T) {
		for _, path := range paths {
			for _, headers := range []map[string]string{
				{},
				{"Authorization": "Bearer " + pat},
				{"Cookie": auth.AuthCookieName + "=bogus"},
				{"X-User-ID": owner, "X-Workspace-ID": workspace, "X-Actor-Source": "task_token", "X-Agent-ID": agent},
				{"Authorization": "Bearer invalid-service-token", "X-User-ID": owner},
			} {
				result := decode(t, call(t, http.MethodPost, path, scope, headers, http.StatusUnauthorized))
				if !result.Handled || !result.Silent || result.Binding != nil || result.CommentID != "" {
					t.Fatalf("authentication failure exposed a relay decision: %+v", result)
				}
			}
		}
	})
	t.Run("missing_or_invalid_service_configuration_disables_all_routes", func(t *testing.T) {
		for _, setting := range []struct{ key, value string }{
			{"INFOFLOW_ISSUE_RELAY_TOKEN", ""},
			{"INFOFLOW_ISSUE_RELAY_TOKEN", "short"},
			{"INFOFLOW_ISSUE_IDENTITY_DOMAIN", ""},
			{"INFOFLOW_ISSUE_IDENTITY_DOMAIN", "invalid/domain"},
		} {
			t.Run(setting.key+"/"+setting.value, func(t *testing.T) {
				t.Setenv(setting.key, setting.value)
				for _, path := range paths {
					call(t, http.MethodPost, path, scope, trustedHeaders, http.StatusServiceUnavailable)
				}
			})
		}
	})
	if got := f.Count(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1`, robot); got != 0 {
		t.Fatalf("rejected HTTP requests wrote %d bindings", got)
	}

	var groupMessage map[string]any
	for _, kind := range []string{"private", "group"} {
		t.Run(kind+"_uses_verified_sender_and_preserves_no_invoke_comment", func(t *testing.T) {
			conversationID := actorSender
			if kind == "group" {
				conversationID = "9223372036854775806"
			}
			body := map[string]any{
				"robot_id": robot, "agent_id": agent, "workspace_id": workspace,
				"conversation": map[string]string{"kind": kind, "id": conversationID},
				"sender":       actorSender, "message_id": kind + "-bind-" + suffix,
				"operation": "bind", "issue_ref": issue,
			}
			bound := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, http.StatusOK))
			if !bound.Handled || bound.Binding == nil || bound.Binding.IssueID != issue || bound.Binding.Generation == "" || bound.Binding.WorkspaceSlug == "" || bound.Binding.Identifier == "" {
				t.Fatalf("service-authenticated binding was not accepted: %+v", bound)
			}
			if kind == "group" {
				if bound.CanComment || bound.Binding.Group == nil || bound.Binding.Group.State != "pending" {
					t.Fatalf("group not pending: %+v", bound)
				}
				memberHeaders := map[string]string{"Authorization": "Bearer " + pat, "X-Workspace-ID": workspace}
				groupPath := "/api/issues/" + issue + "/infoflow-groups"
				call(t, http.MethodGet, groupPath, nil, nil, http.StatusUnauthorized)
				call(t, http.MethodGet, groupPath, nil, trustedHeaders, http.StatusUnauthorized)
				if data := call(t, http.MethodGet, groupPath, nil, memberHeaders, 200); strings.Contains(string(data), conversationID) {
					t.Fatal("pending group exposed")
				}
				call(t, http.MethodPost, "/api/integrations/infoflow/issue-groups", scope, trustedHeaders, 200)
				status := map[string]any{"robot_id": robot, "agent_id": agent, "workspace_id": workspace, "conversation": body["conversation"], "binding_id": bound.Binding.BindingID, "generation": bound.Binding.Generation, "leader_agent_id": agent, "leader_robot_id": robot, "leader_present": true}
				active := decode(t, call(t, http.MethodPost, "/api/integrations/infoflow/issue-group-status", status, trustedHeaders, 200))
				if active.Binding == nil || active.Binding.Group == nil || active.Binding.Group.State != "active" {
					t.Fatalf("status: %+v", active)
				}
				if data := call(t, http.MethodGet, groupPath, nil, memberHeaders, 200); !strings.Contains(string(data), conversationID) || !strings.Contains(string(data), "infoflow://APICenter") {
					t.Fatalf("active link not exposed: %s", data)
				}
			}
			body["operation"], body["message_id"], body["content"] = "message", kind+"-message-"+suffix, "Incoming human comment from "+kind
			delete(body, "issue_ref")
			// The real router must reject missing/stale admission versions before
			// a comment or task is created. Keep these requests deliberately raw.
			for _, pin := range []map[string]string{
				{},
				{"expected_binding_id": bound.Binding.BindingID},
				{"expected_generation": bound.Binding.Generation},
				{"expected_binding_id": uuid.NewString(), "expected_generation": bound.Binding.Generation},
				{"expected_binding_id": bound.Binding.BindingID, "expected_generation": uuid.NewString()},
			} {
				bad := make(map[string]any, len(body)+2)
				for key, value := range body {
					bad[key] = value
				}
				for key, value := range pin {
					bad[key] = value
				}
				call(t, http.MethodPost, relayPath, bad, trustedHeaders, http.StatusConflict)
			}
			if got := f.Count(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND content LIKE $2`, issue, "%Incoming human comment from "+kind+"%"); got != 0 {
				t.Fatalf("invalid message versions created %d comments", got)
			}
			body["expected_binding_id"], body["expected_generation"] = bound.Binding.BindingID, bound.Binding.Generation
			forgedHeaders := map[string]string{
				"Authorization": "Bearer " + serviceToken, "Cookie": auth.AuthCookieName + "=bogus",
				"X-User-ID": owner, "X-Workspace-ID": uuid.NewString(),
				"X-Agent-ID": agent, "X-Task-ID": uuid.NewString(), "X-Daemon-ID": uuid.NewString(),
				"X-Actor-Source": "task_token", "X-Originator-User-ID": owner,
			}
			result := decode(t, call(t, http.MethodPost, relayPath, body, forgedHeaders, http.StatusOK))
			if !result.Handled || !result.CanComment || result.CommentID == "" || result.Binding == nil || result.Binding.IssueID != issue || !strings.Contains(result.Notice, "没有智能体权限") || !strings.Contains(result.Notice, ownerName) {
				t.Fatalf("no-invoke sender must save a comment and receive the owner denial: %+v", result)
			}
			var authorType, authorID, commentWorkspace, content, sourceUser string
			f.QueryRow(t, `SELECT author_type,author_id::text,workspace_id::text,content FROM comment WHERE id=$1 AND issue_id=$2`, result.CommentID, issue).Scan(&authorType, &authorID, &commentWorkspace, &content)
			f.QueryRow(t, `SELECT s.user_id::text FROM infoflow_issue_source s JOIN infoflow_issue_binding b ON b.id=s.binding_id WHERE b.robot_id=$1 AND s.comment_id=$2`, robot, result.CommentID).Scan(&sourceUser)
			if authorType != "member" || authorID != actor || sourceUser != actor || commentWorkspace != workspace || !strings.Contains(content, body["content"].(string)) {
				t.Fatalf("forged headers displaced the real actor: author=%s/%s source=%s workspace=%s", authorType, authorID, sourceUser, commentWorkspace)
			}
			replay := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, http.StatusOK))
			if replay.CommentID != result.CommentID {
				t.Fatalf("message retry created a different comment: %s versus %s", replay.CommentID, result.CommentID)
			}
			if got := f.Count(t, `SELECT count(*) FROM comment WHERE issue_id=$1 AND author_id=$2 AND content LIKE $3`, issue, actor, "%Incoming human comment from "+kind+"%"); got != 1 {
				t.Fatalf("retry saved %d comments; want exactly one", got)
			}
			if got := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issue); got != 0 {
				t.Fatalf("no-invoke admin caused %d tasks", got)
			}
			if kind == "group" {
				groupMessage = body
			}
		})
	}
	t.Run("no_workspace_access_returns_owner_notice_without_comment_or_task", func(t *testing.T) {
		if groupMessage == nil {
			t.Fatal("group binding prerequisite failed")
		}
		body := make(map[string]any, len(groupMessage))
		for key, value := range groupMessage {
			body[key] = value
		}
		body["sender"], body["message_id"] = outsiderSender, "outsider-"+suffix
		before := f.Count(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, issue)
		denied := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, http.StatusOK))
		if !denied.Handled || denied.Silent || denied.CanComment || denied.CommentID != "" || !strings.Contains(denied.Notice, "没有工作区权限") || !strings.Contains(denied.Notice, ownerName) || denied.Binding == nil || denied.Binding.IssueID != issue || denied.Binding.WorkspaceSlug == "" || denied.Binding.Identifier == "" {
			t.Fatalf("workspace denial must identify its owner and supply issue-link metadata: %+v", denied)
		}
		if after := f.Count(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, issue); after != before {
			t.Fatalf("workspace denial wrote a comment: before=%d after=%d", before, after)
		}
		if got := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issue); got != 0 {
			t.Fatalf("workspace denial caused %d tasks", got)
		}
	})
	t.Run("group_multi_issue_routing_and_binding_specific_presence", func(t *testing.T) {
		second := f.Issue(t, "Second HTTP issue", testutil.Cols{"assignee_type": "agent", "assignee_id": agent})
		body := map[string]any{"robot_id": robot, "agent_id": agent, "workspace_id": workspace, "conversation": groupMessage["conversation"], "sender": actorSender, "message_id": "second-bind-" + suffix, "operation": "bind", "issue_ref": second}
		bound := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, 200))
		if bound.Binding == nil || bound.Binding.IssueID != second {
			t.Fatalf("second binding missing: %+v", bound)
		}
		f.Cleanup(t, `DELETE FROM infoflow_issue_binding WHERE id=$1`, bound.Binding.BindingID)
		f.Cleanup(t, `DELETE FROM infoflow_issue_source WHERE binding_id=$1`, bound.Binding.BindingID)
		status := map[string]any{"robot_id": robot, "agent_id": agent, "workspace_id": workspace, "conversation": body["conversation"], "generation": bound.Binding.Generation, "leader_agent_id": agent, "leader_robot_id": robot, "leader_present": true}
		call(t, http.MethodPost, "/api/integrations/infoflow/issue-group-status", status, trustedHeaders, 400)
		status["binding_id"] = bound.Binding.BindingID
		call(t, http.MethodPost, "/api/integrations/infoflow/issue-group-status", status, trustedHeaders, 200)
		for _, task := range []string{issue, second} {
			data := call(t, http.MethodGet, "/api/issues/"+task+"/infoflow-groups", nil, map[string]string{"Authorization": "Bearer " + pat, "X-Workspace-ID": workspace}, 200)
			var links struct {
				Groups []map[string]string `json:"groups"`
			}
			if json.Unmarshal(data, &links) != nil || len(links.Groups) != 1 || links.Groups[0]["group_id"] != "9223372036854775806" {
				t.Fatalf("issue group response must remain a singleton array: %s", data)
			}
		}
		body["operation"], body["message_id"], body["content"] = "binding", "ambiguous-"+suffix, "continue"
		delete(body, "issue_ref")
		ambiguous := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, 200))
		if !ambiguous.Handled || ambiguous.CanComment || ambiguous.Binding != nil || !strings.Contains(ambiguous.Notice, "多个任务") {
			t.Fatalf("ambiguous HTTP request fell through: %+v", ambiguous)
		}
		body["content"] = "继续 " + bound.Binding.Identifier
		selected := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, 200))
		if selected.Binding == nil || selected.Binding.IssueID != second {
			t.Fatalf("explicit HTTP route failed: %+v", selected)
		}
		body["operation"], body["command"], body["issue_ref"] = "unbind", "unbind", bound.Binding.Identifier
		call(t, http.MethodPost, relayPath, body, trustedHeaders, 409)
		body["expected_binding_id"], body["expected_generation"] = bound.Binding.BindingID, bound.Binding.Generation
		unbound := decode(t, call(t, http.MethodPost, relayPath, body, trustedHeaders, 200))
		if unbound.Binding == nil || unbound.Binding.IssueID != second {
			t.Fatalf("unbind target: %+v", unbound)
		}
		if f.Count(t, `SELECT count(*) FROM infoflow_issue_binding WHERE conversation_kind='group' AND workspace_id=$1 AND issue_id=$2`, workspace, issue) != 1 {
			t.Fatal("second unbind removed first HTTP binding")
		}
	})
	t.Run("membership_revocation_precedes_cached_response", func(t *testing.T) {
		if groupMessage == nil {
			t.Fatal("group message prerequisite failed")
		}
		f.Exec(t, `DELETE FROM member WHERE id=$1`, actorMembership)
		denied := decode(t, call(t, http.MethodPost, relayPath, groupMessage, trustedHeaders, http.StatusOK))
		if !denied.Handled || denied.CanComment || denied.CommentID != "" || !strings.Contains(denied.Notice, "没有工作区权限") {
			t.Fatalf("cached mutation bypassed revoked membership: %+v", denied)
		}
	})
}
