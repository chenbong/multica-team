package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func newInfoFlowWebFixture(t *testing.T) infoFlowTestFixture {
	f := newInfoFlowFixture(t)
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `DELETE FROM infoflow_web_group_request WHERE issue_id=$1`, f.issue)
		if err != nil {
			t.Errorf("web request cleanup: %v", err)
		}
		_, err = testPool.Exec(context.Background(), `DELETE FROM infoflow_group_observation WHERE workspace_id=$1 AND agent_id=$2`, f.req.WorkspaceID, f.req.AgentID)
		if err != nil {
			t.Errorf("group observation cleanup: %v", err)
		}
	})
	return f
}

func infoFlowWebCall(t *testing.T, f infoFlowTestFixture, method, user string, body any, status int) infoFlowWebView {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest(method, "/web-group", body), "X-User-ID", user, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue)
	h := f.h.GetIssueInfoFlowGroupBinding
	if method == "POST" {
		h = f.h.BindIssueInfoFlowGroup
	}
	c := testutil.Call(t, h, r).Want(status)
	var out infoFlowWebView
	if status == 200 || status == 202 {
		c.JSON(&out)
	}
	return out
}

type infoFlowClaimedJob struct {
	RequestID  string               `json:"request_id"`
	Generation string               `json:"generation"`
	LeaseToken string               `json:"lease_token"`
	GroupID    string               `json:"group_id"`
	Sender     string               `json:"sender"`
	Inviters   []string             `json:"inviter_robot_ids"`
	Binding    *infoFlowBindingView `json:"binding"`
}

func infoFlowWebClaim(t *testing.T, f infoFlowTestFixture) []infoFlowClaimedJob {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/jobs", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
	var out struct {
		Jobs []infoFlowClaimedJob `json:"jobs"`
	}
	testutil.Call(t, f.h.InfoFlowWebGroupJobs, r).Want(200).JSON(&out)
	return out.Jobs
}

func infoFlowWebSuccess(f infoFlowTestFixture, j infoFlowClaimedJob) infoFlowWebResult {
	return infoFlowWebResult{infoFlowScope: f.req.infoFlowScope, RequestID: j.RequestID, Generation: j.Generation, LeaseToken: j.LeaseToken, GroupID: j.GroupID, ActorPresent: true, LeaderPresent: true, LeaderAgentID: j.Binding.Group.LeaderAgentID, LeaderRobotID: f.req.RobotID}
}

func infoFlowWebResultCall(t *testing.T, f infoFlowTestFixture, req infoFlowWebResult, status int) string {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/result", req), "Authorization", "Bearer "+infoFlowTestToken)
	c := testutil.Call(t, f.h.InfoFlowWebGroupResult, r).Want(status)
	var out struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
	}
	if status == 200 {
		c.JSON(&out)
		if !out.OK {
			t.Fatal("result not accepted")
		}
	}
	return out.State
}

func infoFlowWebObserve(t *testing.T, f infoFlowTestFixture, group string, members []string) {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/observation", infoFlowGroupObservation{infoFlowScope: f.req.infoFlowScope, GroupID: group, Name: "Group fixture", Members: members}), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowGroupObservation, r).Want(200)
}

func TestInfoFlowWebGroupVerifiedAdmission(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	view := infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	if !view.CanManage || view.Binding != nil || view.Request == nil || view.Request.State != "queued" {
		t.Fatalf("premature bind: %+v", view)
	}
	if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue); n != 0 {
		t.Fatal("unverified request created binding")
	}
	jobs := infoFlowWebClaim(t, f)
	if len(jobs) != 1 || jobs[0].Binding.Group.LeaderAgentID != f.req.AgentID || jobs[0].Sender != f.req.Sender {
		t.Fatalf("job: %+v", jobs)
	}
	if len(infoFlowWebClaim(t, f)) != 0 {
		t.Fatal("same request leased twice")
	}
	pending := infoFlowWebSuccess(f, jobs[0])
	pending.LeaderPresent = false
	pending.Reason = "approval_pending"
	if state := infoFlowWebResultCall(t, f, pending, 200); state != "pending" {
		t.Fatal(state)
	}
	view = infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if view.Binding != nil || view.Request == nil || !strings.Contains(view.Request.Reason, "审批") {
		t.Fatalf("approval reported success: %+v", view)
	}
	before := view.Request.RequestID
	view = infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	if view.Request.RequestID != before {
		t.Fatal("repeated submission reset request")
	}
	dbfx.Exec(t, `UPDATE infoflow_web_group_request SET next_at=clock_timestamp()-interval '1 second' WHERE issue_id=$1`, f.issue)
	next := infoFlowWebClaim(t, f)
	if len(next) != 1 || next[0].Generation != jobs[0].Generation || next[0].LeaseToken == jobs[0].LeaseToken {
		t.Fatalf("lease retry: %+v", next)
	}
	infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, jobs[0]), 409)
	if state := infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, next[0]), 200); state != "active" {
		t.Fatal(state)
	}
	view = infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if view.Binding == nil || view.Binding.State != "active" || view.Request != nil {
		t.Fatalf("verified bind absent: %+v", view)
	}
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 200)
	if n := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue); n != 1 {
		t.Fatal("duplicate binding")
	}
	for _, table := range []string{"comment", "agent_task_queue"} {
		if infoFlowCount(t, `SELECT count(*) FROM `+table+` WHERE issue_id=$1`, f.issue) != 0 {
			t.Fatal("web bind executed a task")
		}
	}
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET leader_verified_at=clock_timestamp()-interval '46 seconds' WHERE issue_id=$1`, f.issue)
	view = infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if view.Binding == nil || view.Binding.State != "pending" || view.Request != nil {
		t.Fatalf("stale success: %+v", view)
	}
}

func TestInfoFlowWebGroupSelectionPrivacyAndPermission(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
	infoFlowWebObserve(t, f, f.req.Conversation.ID, []string{f.req.Sender})
	other := "9223372036854775807"
	infoFlowWebObserve(t, f, other, []string{"someoneelse"})
	view := infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if len(view.Groups) != 1 || view.Groups[0].GroupID != f.req.Conversation.ID {
		t.Fatalf("candidate leak: %+v", view.Groups)
	}
	view = infoFlowWebCall(t, f, "GET", f.user, nil, 200)
	if view.CanManage || len(view.Groups) != 0 {
		t.Fatal("member gained group management")
	}
	infoFlowWebCall(t, f, "POST", f.user, map[string]string{"group_id": other}, 403)
	infoFlowWebCall(t, f, "GET", uuid.NewString(), nil, 404)
	dbfx.Exec(t, `UPDATE infoflow_group_observation SET observed_at=clock_timestamp()-interval '25 hours' WHERE agent_id=$1`, f.req.AgentID)
	if len(infoFlowWebCall(t, f, "GET", f.owner, nil, 200).Groups) != 0 {
		t.Fatal("expired discovery leaked")
	}
	t.Setenv("INFOFLOW_ISSUE_RELAY_TOKEN", "")
	view = infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if view.Enabled || view.CanManage || view.Request != nil || len(view.Groups) != 0 {
		t.Fatal("disabled feature leaked metadata")
	}
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": other}, 503)
}

func TestInfoFlowWebGroupMalformedAndHumanOnly(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	for _, body := range []any{
		map[string]any{"group_id": 543210}, map[string]string{"group_id": "0"}, map[string]string{"group_id": "01"}, map[string]string{"group_id": "-1"}, map[string]string{"group_id": "9223372036854775808"}, map[string]string{"group_id": "https://example.test"}, map[string]string{"group_id": "100", "sender": "someoneelse"},
	} {
		infoFlowWebCall(t, f, "POST", f.owner, body, 400)
	}
	for _, source := range []string{"task_token", "cloud_pat"} {
		for _, method := range []string{"GET", "POST"} {
			r := testutil.WithHeaders(testutil.JSONRequest(method, "/web", map[string]string{"group_id": "123"}), "X-Actor-Source", source, "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID)
			h := f.h.GetIssueInfoFlowGroupBinding
			if method == "POST" {
				h = f.h.BindIssueInfoFlowGroup
			}
			testutil.Call(t, h, r).Want(403)
		}
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_web_group_request WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("rejected mutation wrote request")
	}
}

func TestInfoFlowWebGroupRejectStaleOrRevokedJob(t *testing.T) {
	for _, change := range []string{"membership", "owner", "assignee", "archive", "expiry", "lease", "scope", "generation", "leader", "actor", "malformed_reason"} {
		t.Run(change, func(t *testing.T) {
			f := newInfoFlowWebFixture(t)
			infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
			jobs := infoFlowWebClaim(t, f)
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			req := infoFlowWebSuccess(f, jobs[0])
			want := 200
			switch change {
			case "membership":
				dbfx.Exec(t, `DELETE FROM member WHERE user_id=$1 AND workspace_id=$2`, f.owner, f.req.WorkspaceID)
			case "owner":
				dbfx.Exec(t, `UPDATE member SET role='member' WHERE user_id=$1 AND workspace_id=$2`, f.owner, f.req.WorkspaceID)
				dbfx.Exec(t, `UPDATE agent SET owner_id=$2 WHERE id=$1`, f.req.AgentID, f.user)
			case "assignee":
				dbfx.Exec(t, `UPDATE issue SET assignee_id=NULL,assignee_type=NULL WHERE id=$1`, f.issue)
			case "archive":
				dbfx.Exec(t, `UPDATE agent SET archived_at=clock_timestamp() WHERE id=$1`, f.req.AgentID)
				want = 400
			case "expiry":
				dbfx.Exec(t, `UPDATE infoflow_web_group_request SET expires_at=clock_timestamp()-interval '1 second' WHERE issue_id=$1`, f.issue)
				want = 409
			case "lease":
				dbfx.Exec(t, `UPDATE infoflow_web_group_request SET lease_until=clock_timestamp()-interval '1 second' WHERE issue_id=$1`, f.issue)
				want = 409
			case "scope":
				req.RobotID = uuid.NewString()
				want = 409
			case "generation":
				req.Generation = uuid.NewString()
				want = 409
			case "leader":
				req.LeaderAgentID = uuid.NewString()
				want = 409
			case "actor":
				req.ActorPresent = false
				want = 400
			case "malformed_reason":
				req.Reason = "active"
				want = 400
			}
			if state := infoFlowWebResultCall(t, f, req, want); want == 200 && state != "failed" {
				t.Fatalf("revoked job activated: %s", state)
			}
			if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue) != 0 {
				t.Fatal("invalid result created binding")
			}
		})
	}
}

func TestInfoFlowWebGroupActorAbsentNoBindingAndRetry(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	jobs := infoFlowWebClaim(t, f)
	req := infoFlowWebSuccess(f, jobs[0])
	req.ActorPresent = false
	req.LeaderPresent = false
	req.Reason = "actor_not_in_group"
	if state := infoFlowWebResultCall(t, f, req, 200); state != "failed" {
		t.Fatal(state)
	}
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	next := infoFlowWebClaim(t, f)
	if len(next) != 1 || next[0].Generation == jobs[0].Generation {
		t.Fatal("explicit retry failed to replace generation")
	}
	infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, jobs[0]), 409)
}

func TestInfoFlowWebGroupReservationAndConcurrentClaims(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": "200"}, 409)
	for _, group := range []string{f.req.Conversation.ID, "200"} {
		req := f.req
		req.Conversation.ID = group
		req.MessageID = uuid.NewString()
		out := infoFlowCall(t, f.h, req, 200)
		if out.Binding != nil || !strings.Contains(out.Notice, "网页") {
			t.Fatalf("IM bypassed pending reservation: %+v", out)
		}
	}
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			r := testutil.WithHeaders(testutil.JSONRequest("POST", "/jobs", f.req.infoFlowScope), "Authorization", "Bearer "+infoFlowTestToken)
			w := httptest.NewRecorder()
			f.h.InfoFlowWebGroupJobs(w, r)
			done <- w
		}()
	}
	close(start)
	leased := 0
	for range 2 {
		select {
		case w := <-done:
			var out struct {
				Jobs []infoFlowClaimedJob `json:"jobs"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			leased += len(out.Jobs)
		case <-time.After(10 * time.Second):
			t.Fatal("claims deadlocked")
		}
	}
	if leased != 1 {
		t.Fatalf("claimed %d jobs", leased)
	}
}

func TestInfoFlowWebGroupExistingUnbindCannotResurrect(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	bound := f.bind(t)
	dbfx.Exec(t, `UPDATE infoflow_issue_binding SET leader_verified_at=clock_timestamp()-interval '46 seconds' WHERE issue_id=$1`, f.issue)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	jobs := infoFlowWebClaim(t, f)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	req := f.req
	req.Operation = "unbind"
	req.MessageID = uuid.NewString()
	req.ExpectedBindingID = bound.Binding.BindingID
	req.ExpectedGeneration = bound.Binding.Generation
	infoFlowCall(t, f.h, req, 200)
	infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, jobs[0]), 409)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("removed task rebound")
	}
}

func TestInfoFlowWebGroupServiceBoundary(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	for _, handler := range []http.HandlerFunc{f.h.InfoFlowGroupObservation, f.h.InfoFlowWebGroupJobs, f.h.InfoFlowWebGroupResult} {
		for _, token := range []string{"", "mul_fake_ordinary_token", "invalid"} {
			r := testutil.WithHeaders(testutil.JSONRequest("POST", "/trusted", f.req.infoFlowScope), "Authorization", "Bearer "+token, "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID)
			testutil.Call(t, handler, r).Want(401)
		}
	}
	bad := infoFlowGroupObservation{infoFlowScope: f.req.infoFlowScope, GroupID: f.req.Conversation.ID, Members: []string{"invalid@account"}}
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/observation", bad), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowGroupObservation, r).Want(400)
}

func TestInfoFlowWebGroupLeaseExpiresWhileWaitingOnAuthority(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	jobs := infoFlowWebClaim(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hold, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	var pid int
	if err = hold.QueryRow(ctx, `SELECT pg_backend_pid() FROM member WHERE user_id=$1 AND workspace_id=$2 FOR UPDATE`, f.owner, f.req.WorkspaceID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE infoflow_web_group_request SET lease_until=clock_timestamp()+interval '1 second' WHERE issue_id=$1`, f.issue)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := testutil.WithHeaders(testutil.JSONRequest("POST", "/result", infoFlowWebSuccess(f, jobs[0])), "Authorization", "Bearer "+infoFlowTestToken).WithContext(ctx)
		w := httptest.NewRecorder()
		f.h.InfoFlowWebGroupResult(w, r)
		done <- w
	}()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("result did not wait for authority lock")
	}
	for time.Now().Before(deadline) {
		var expired bool
		if err = testPool.QueryRow(ctx, `SELECT lease_until<clock_timestamp() FROM infoflow_web_group_request WHERE issue_id=$1`, f.issue).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 409 {
			t.Fatalf("expired proof accepted: %d %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("result remained blocked")
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue) != 0 {
		t.Fatal("stale membership proof activated binding")
	}
}

func TestInfoFlowWebGroupRenameKeepsPendingGeneration(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	jobs := infoFlowWebClaim(t, f)
	req := infoFlowWebSuccess(f, jobs[0])
	req.LeaderPresent = false
	req.Reason = "approval_pending"
	infoFlowWebResultCall(t, f, req, 200)
	dbfx.Exec(t, `UPDATE agent SET name='Renamed fixture leader' WHERE id=$1`, f.req.AgentID)
	dbfx.Exec(t, `UPDATE infoflow_web_group_request SET next_at=clock_timestamp()-interval '1 second' WHERE issue_id=$1`, f.issue)
	next := infoFlowWebClaim(t, f)
	if len(next) != 1 || next[0].Generation != jobs[0].Generation || next[0].Binding.Group.AgentNames[f.req.AgentID] != "Renamed fixture leader" {
		t.Fatalf("rename broke pending request: %+v", next)
	}
	infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, next[0]), 200)
}

func TestInfoFlowWebGroupPendingBlocksIssueTransfer(t *testing.T) {
	f := newTransferFixture(t)
	id := dbfx.Insert(t, "infoflow_web_group_request", testutil.Cols{"id": uuid.NewString(), "issue_id": f.issue, "workspace_id": f.source, "group_id": "543210", "requester_id": f.actor, "generation": uuid.NewString(), "topology": "{}", "state": "queued", "expires_at": testutil.Raw("clock_timestamp()+interval '10 minutes'")})
	p, err := f.run(issueTransferRequest{}, false)
	if err != nil || !containsTransferBlocker(p, "infoflow_pending_binding") {
		t.Fatalf("pending web binding moved: %+v %v", p, err)
	}
	dbfx.Exec(t, `UPDATE infoflow_web_group_request SET state='failed' WHERE id=$1`, id)
	p, err = f.run(issueTransferRequest{}, false)
	if err != nil || containsTransferBlocker(p, "infoflow_pending_binding") {
		t.Fatal("failed request incorrectly blocked transfer", err)
	}
}

func TestInfoFlowWebGroupOldFailureDoesNotHideNewIMBinding(t *testing.T) {
	f := newInfoFlowWebFixture(t)
	infoFlowWebCall(t, f, "POST", f.owner, map[string]string{"group_id": f.req.Conversation.ID}, 202)
	jobs := infoFlowWebClaim(t, f)
	req := infoFlowWebSuccess(f, jobs[0])
	req.ActorPresent = false
	req.LeaderPresent = false
	req.Reason = "actor_not_in_group"
	infoFlowWebResultCall(t, f, req, 200)
	im := f.req
	im.Conversation.ID = "543211"
	im.MessageID = uuid.NewString()
	bound := infoFlowCall(t, f.h, im, 200)
	if bound.Binding == nil {
		t.Fatal("IM did not bind after terminal failure")
	}
	view := infoFlowWebCall(t, f, "GET", f.owner, nil, 200)
	if view.Binding == nil || view.Binding.GroupID != im.Conversation.ID || view.Request != nil {
		t.Fatalf("stale failure contradicted new binding: %+v", view)
	}
}

type infoFlowReadFenceStarter struct {
	before func(context.Context)
}

func (s infoFlowReadFenceStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	s.before(ctx)
	return testPool.Begin(ctx)
}

func TestInfoFlowWebGroupReadFenceIncludesCompatibilityRoute(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "compatibility"}[legacy], func(t *testing.T) {
			f := newInfoFlowWebFixture(t)
			f.bind(t)
			f.h.TxStarter = infoFlowReadFenceStarter{before: func(ctx context.Context) {
				if _, err := testPool.Exec(ctx, `DELETE FROM member WHERE id=$1`, f.member); err != nil {
					t.Error(err)
				}
			}}
			if legacy {
				infoFlowReadGroups(t, f, f.user, 404)
			} else {
				infoFlowWebCall(t, f, "GET", f.user, nil, 404)
			}
		})
	}
}
