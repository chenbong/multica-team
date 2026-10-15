package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func infoFlowCancelRequestCall(t *testing.T, f infoFlowTestFixture, user, kind, id, generation string, status int) infoFlowConversationsView {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/requests", map[string]string{"kind": kind, "expected_generation": generation}), "X-User-ID", user, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "requestId", id)
	c := testutil.Call(t, f.h.CancelIssueInfoFlowRequest, r).Want(status)
	var out infoFlowConversationsView
	if status == 200 {
		c.JSON(&out)
	}
	return out
}

func infoFlowPendingRequest(t *testing.T, f infoFlowTestFixture, kind string) (string, string) {
	t.Helper()
	body := map[string]string{"kind": kind}
	if kind == "group" {
		body["group_id"] = f.req.Conversation.ID
	}
	v := infoFlowConversationCall(t, f, "POST", f.owner, "", body, 202)
	if kind == "group" {
		if v.Group.Request == nil || !v.Group.Request.CanCancel {
			t.Fatal("missing group cancellation")
		}
		return v.Group.Request.RequestID, v.Group.Request.Generation
	}
	if v.Private.Request == nil || !v.Private.Request.CanCancel {
		t.Fatal("missing private cancellation")
	}
	return v.Private.Request.RequestID, v.Private.Request.Generation
}

func TestInfoFlowPrivateWebRequestCannotSurviveIMBindingChange(t *testing.T) {
	for _, operation := range []string{"bind", "bind_unbind"} {
		t.Run(operation, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "private"}, 202)
			jobs := infoFlowPrivateClaimCall(t, f)
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			bound := f.bind(t)
			if operation == "bind_unbind" {
				unbind := f.req
				unbind.Operation, unbind.MessageID = "unbind", uuid.NewString()
				unbind.ExpectedBindingID, unbind.ExpectedGeneration = bound.Binding.BindingID, bound.Binding.Generation
				if got := infoFlowCall(t, f.h, unbind, 200); got.ConversationMode != "chat" {
					t.Fatal("unbind did not restore Chat")
				}
			}
			infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, jobs[0]), 409)
			var state string
			dbfx.QueryRow(t, `SELECT state FROM infoflow_web_private_request WHERE issue_id=$1`, f.issue).Scan(&state)
			if state != "failed" {
				t.Fatal("explicit IM mutation did not invalidate pending request")
			}
			if operation == "bind_unbind" && infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue) != 0 {
				t.Fatal("late result revived unbound task")
			}
		})
	}
}

func TestInfoFlowCancelRequestAndRetryAreVersionedForBothKinds(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			id, gen := infoFlowPendingRequest(t, f, kind)
			var groupJob infoFlowClaimedJob
			var privateJob infoFlowPrivateClaim
			if kind == "group" {
				jobs := infoFlowWebClaim(t, f)
				if len(jobs) != 1 {
					t.Fatal(jobs)
				}
				groupJob = jobs[0]
			} else {
				jobs := infoFlowPrivateClaimCall(t, f)
				if len(jobs) != 1 {
					t.Fatal(jobs)
				}
				privateJob = jobs[0]
			}
			out := infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 200)
			if kind == "group" {
				if out.Group.Binding != nil || out.Group.Request.CanCancel || out.Group.Request.Reason != "申请已取消，可重新提交" {
					t.Fatal("group cancellation state incorrect")
				}
			} else {
				if len(out.Private.Bindings) != 0 || out.Private.Request.CanCancel || out.Private.Request.Reason != "申请已取消，可重新提交" {
					t.Fatal("private cancellation state incorrect")
				}
			}
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 200)
			if kind == "group" {
				infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, groupJob), 409)
			} else {
				infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, privateJob), 409)
			}
			newID, newGen := infoFlowPendingRequest(t, f, kind)
			if newID != id || newGen == gen {
				t.Fatal("retry did not preserve identity and rotate admission version")
			}
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 409)
			if kind == "group" {
				jobs := infoFlowWebClaim(t, f)
				if len(jobs) != 1 {
					t.Fatal("old cancel removed new request")
				}
				if infoFlowWebResultCall(t, f, infoFlowWebSuccess(f, jobs[0]), 200) != "active" {
					t.Fatal("fresh group request could not bind")
				}
			} else {
				jobs := infoFlowPrivateClaimCall(t, f)
				if len(jobs) != 1 {
					t.Fatal("old cancel removed new request")
				}
				if infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, jobs[0]), 200) != "active" {
					t.Fatal("fresh private request could not bind")
				}
			}
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, newGen, 409)
			if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 0 || infoFlowCount(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issue) != 0 {
				t.Fatal("cancellation created work")
			}
		})
	}
}

func TestInfoFlowCancelGroupRequestReleasesWrongTarget(t *testing.T) {
	f := infoFlowConversationFixture(t)
	id, gen := infoFlowPendingRequest(t, f, "group")
	infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "group", "group_id": "543210"}, 409)
	infoFlowCancelRequestCall(t, f, f.owner, "group", id, gen, 200)
	v := infoFlowConversationCall(t, f, "POST", f.owner, "", map[string]string{"kind": "group", "group_id": "543210"}, 202)
	if v.Group.Request.GroupID != "543210" || v.Group.Request.Generation == gen {
		t.Fatal("cancel did not release reserved group")
	}
}

func TestInfoFlowIMInvalidationRollsBackWithFailedSourceReceipt(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	bound := f.bind(t)
	infoFlowPendingRequest(t, f, "private")
	jobs := infoFlowPrivateClaimCall(t, f)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	request := f.req
	request.Operation, request.MessageID = "unbind", uuid.NewString()
	request.ExpectedBindingID, request.ExpectedGeneration = bound.Binding.BindingID, bound.Binding.Generation
	original := f.h.TxStarter
	f.h.TxStarter = infoFlowFailSourceStarter{base: original}
	infoFlowCall(t, f.h, request, 500)
	f.h.TxStarter = original
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_web_private_request WHERE id=$1 AND generation=$2 AND state='pending'`, jobs[0].RequestID, jobs[0].Generation) != 1 {
		t.Fatal("failed unbind committed admission invalidation")
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2`, bound.Binding.BindingID, f.issue) != 1 {
		t.Fatal("failed unbind removed association")
	}
	if infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, jobs[0]), 200) != "active" {
		t.Fatal("rolled-back mutation blocked current admission")
	}
}

func TestInfoFlowOldUnbindReplayDoesNotCancelNewExplicitWebIntent(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
	bound := f.bind(t)
	request := f.req
	request.Operation, request.MessageID = "unbind", uuid.NewString()
	request.ExpectedBindingID, request.ExpectedGeneration = bound.Binding.BindingID, bound.Binding.Generation
	infoFlowCall(t, f.h, request, 200)
	infoFlowPendingRequest(t, f, "private")
	jobs := infoFlowPrivateClaimCall(t, f)
	if len(jobs) != 1 {
		t.Fatal(jobs)
	}
	infoFlowCall(t, f.h, request, 200)
	if infoFlowPrivateResultCall(t, f, infoFlowPrivateResultFor(f, jobs[0]), 200) != "active" {
		t.Fatal("old receipt replay overrode newer user intent")
	}
}

func TestInfoFlowRequestCancellationPermissions(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			id, gen := infoFlowPendingRequest(t, f, kind)
			dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
			infoFlowCancelRequestCall(t, f, f.user, kind, id, gen, 403)
			dbfx.Exec(t, `UPDATE member SET role='admin' WHERE id=$1`, f.member)
			if kind == "private" {
				infoFlowCancelRequestCall(t, f, f.user, kind, id, gen, 403)
			} else {
				infoFlowCancelRequestCall(t, f, f.user, kind, id, gen, 200)
				return
			}
			dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.req.WorkspaceID, f.owner)
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 404)
		})
	}
}

func TestInfoFlowOwnRequestRemainsCancellableAfterLosingBindingEligibility(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			id, gen := infoFlowPendingRequest(t, f, kind)
			dbfx.Exec(t, `UPDATE member SET role='member' WHERE workspace_id=$1 AND user_id=$2`, f.req.WorkspaceID, f.owner)
			dbfx.Exec(t, `UPDATE issue SET assignee_type=NULL,assignee_id=NULL WHERE id=$1`, f.issue)
			v := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
			if kind == "group" {
				if v.Group.CanManage || v.Group.Request == nil || !v.Group.Request.CanCancel || len(v.Group.Groups) != 0 {
					t.Fatal("cancellation granted group binding privilege")
				}
			} else {
				if v.Private.Eligible || v.Private.CanManage || v.Private.Request == nil || !v.Private.Request.CanCancel {
					t.Fatal("cannot cancel invalidated private request")
				}
			}
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 200)
		})
	}
}

func TestInfoFlowCancelDoesNotRemoveExistingBinding(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			}
			bound := f.bind(t)
			if kind == "group" {
				dbfx.Exec(t, `UPDATE infoflow_issue_binding SET leader_verified_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, bound.Binding.BindingID)
			}
			id, gen := infoFlowPendingRequest(t, f, kind)
			infoFlowCancelRequestCall(t, f, f.owner, kind, id, gen, 200)
			if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE id=$1 AND issue_id=$2`, bound.Binding.BindingID, f.issue) != 1 {
				t.Fatal("cancelling admission unbound existing association")
			}
		})
	}
}

func TestInfoFlowCancellationRejectsMalformedAndMachineRequests(t *testing.T) {
	f := infoFlowConversationFixture(t)
	id, gen := infoFlowPendingRequest(t, f, "private")
	for _, body := range []any{map[string]string{"kind": "other", "expected_generation": gen}, map[string]string{"kind": "private"}, map[string]string{"kind": "private", "expected_generation": "bad"}, map[string]string{"kind": "private", "expected_generation": gen, "sender": "forged"}} {
		r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/requests", body), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "requestId", id)
		testutil.Call(t, f.h.CancelIssueInfoFlowRequest, r).Want(400)
	}
	for _, actor := range []string{"task_token", "cloud_pat"} {
		r := testutil.WithHeaders(testutil.JSONRequest("DELETE", "/requests", nil), "X-Actor-Source", actor)
		testutil.Call(t, f.h.CancelIssueInfoFlowRequest, r).Want(403)
	}
	infoFlowCancelRequestCall(t, f, f.owner, "private", uuid.NewString(), gen, 404)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_web_private_request WHERE id=$1 AND state='queued'`, id) != 1 {
		t.Fatal("rejected cancel changed request")
	}
}

func TestInfoFlowCancellationAndResultAreSerialized(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := infoFlowConversationFixture(t)
			id, gen := infoFlowPendingRequest(t, f, kind)
			var resultBody any
			resultHandler := f.h.InfoFlowWebGroupResult
			if kind == "group" {
				resultBody = infoFlowWebSuccess(f, infoFlowWebClaim(t, f)[0])
			} else {
				resultBody = infoFlowPrivateResultFor(f, infoFlowPrivateClaimCall(t, f)[0])
				resultHandler = f.h.InfoFlowWebPrivateResult
			}
			blocker, err := testPool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if kind == "group" {
				err = infoFlowWebLock(context.Background(), blocker, f.req.WorkspaceID, f.req.Conversation.ID, parseUUID(f.issue))
			} else {
				err = infoFlowPrivateWebLock(context.Background(), blocker, parseUUID(f.issue), parseUUID(f.owner))
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct {
				kind   string
				status int
			}, 2)
			go func() {
				r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("DELETE", "/requests", map[string]string{"kind": kind, "expected_generation": gen}), "X-User-ID", f.owner, "X-Workspace-ID", f.req.WorkspaceID), "id", f.issue, "requestId", id)
				w := httptest.NewRecorder()
				f.h.CancelIssueInfoFlowRequest(w, r)
				done <- struct {
					kind   string
					status int
				}{"cancel", w.Code}
			}()
			go func() {
				r := testutil.WithHeaders(testutil.JSONRequest("POST", "/result", resultBody), "Authorization", "Bearer "+infoFlowTestToken)
				w := httptest.NewRecorder()
				resultHandler(w, r)
				done <- struct {
					kind   string
					status int
				}{"result", w.Code}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting int
				if err = testPool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("cancel and result did not reach admission lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err = blocker.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			codes := map[string]int{}
			for range 2 {
				select {
				case item := <-done:
					codes[item.kind] = item.status
				case <-time.After(5 * time.Second):
					t.Fatal("cancel/result deadlocked")
				}
			}
			if !(codes["cancel"] == 200 && codes["result"] == 409 || codes["cancel"] == 409 && codes["result"] == 200) {
				t.Fatalf("inconsistent race outcome: %v", codes)
			}
			bindings := infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE issue_id=$1`, f.issue)
			if codes["cancel"] == 200 && bindings != 0 || codes["result"] == 200 && bindings != 1 {
				t.Fatal("cancel result violated linearization")
			}
		})
	}
}
