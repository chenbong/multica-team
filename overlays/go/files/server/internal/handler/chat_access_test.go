package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func sharedChatFixture(t *testing.T) (agent, session, peer string) {
	t.Helper()
	peer = dbfx.User(t, "Chat Participant", fmt.Sprintf("chat-%s@multica.test", util.UUIDToString(dbid.NewV7())))
	dbfx.Member(t, testWorkspaceID, peer, "member")
	runtime := dbfx.Runtime(t, "Shared chat fake runtime", testutil.Cols{"visibility": "public"})
	agent = dbfx.Agent(t, "Shared chat agent", runtime, testutil.Cols{"permission_mode": "public_to"})
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": agent, "target_type": "workspace", "target_id": testWorkspaceID})
	session = dbfx.ChatSession(t, agent, testutil.Cols{"explicitly_created_at": testutil.Raw("now()"), "title": "Other member's chat"})
	return
}

func sharedChatRequest(t *testing.T, user, session, method string, body any) *http.Request {
	t.Helper()
	r := newRequestAs(user, method, "/api/chat/sessions/"+session, body)
	// A malicious client may present somebody else's member context. The chat
	// guard re-reads membership for the authenticated user rather than trust it.
	r = withChatTestWorkspaceCtx(t, r)
	return withURLParam(r, "sessionId", session)
}

func TestSharedChatListReadSendAndAttribution(t *testing.T) {
	if testHandler == nil {
		t.Skip("test database unavailable")
	}
	agent, session, peer := sharedChatFixture(t)
	ctx := context.Background()
	for name, handler := range map[string]http.HandlerFunc{"detail": testHandler.GetChatSession, "messages": testHandler.ListChatMessages, "page": testHandler.ListChatMessagesPage, "pending": testHandler.GetPendingChatTask} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler(w, sharedChatRequest(t, peer, session, "GET", nil))
			if w.Code != 200 {
				t.Fatalf("read = %d %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	testHandler.ListChatSessions(w, sharedChatRequest(t, peer, session, "GET", nil))
	var sessions []ChatSessionResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sessions) != nil {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, row := range sessions {
		if row.ID == session {
			found = true
			if row.HasUnread || row.Pinned {
				t.Fatal("creator's personal state leaked to peer")
			}
		}
	}
	if !found {
		t.Fatal("accessible peer chat missing from list")
	}
	w = httptest.NewRecorder()
	testHandler.SendChatMessage(w, sharedChatRequest(t, peer, session, "POST", map[string]any{"content": "participant's message"}))
	if w.Code != 201 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var sent SendChatMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	task, err := testHandler.Queries.GetAgentTask(ctx, util.MustParseUUID(sent.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	for label, id := range map[string]string{"initiator": uuidToString(task.InitiatorUserID), "originator": uuidToString(task.OriginatorUserID), "accountable": uuidToString(task.AccountableUserID)} {
		if id != peer {
			t.Fatalf("%s = %s, want actual participant %s", label, id, peer)
		}
	}
	dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, sent.TaskID)
	dbfx.Cleanup(t, `DELETE FROM chat_message WHERE task_id=$1`, sent.TaskID)
	w = httptest.NewRecorder()
	testHandler.ListChatMessages(w, sharedChatRequest(t, peer, session, "GET", nil))
	var messages []ChatMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Sender == nil || messages[0].Sender.UserID != peer || messages[0].Sender.Name != "Chat Participant" {
		t.Fatalf("actual sender missing: %+v", messages)
	}
	// Steering/coalescing may attach another member's input to the SAME task.
	// The task initiator remains peer; the new input author must not become peer.
	coauthor := dbfx.User(t, "Coauthor", "chat-coauthor@multica.test")
	dbfx.Member(t, testWorkspaceID, coauthor, "member")
	steered, err := testHandler.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{ChatSessionID: util.MustParseUUID(session), Role: "user", Content: "coalesced second input", TaskID: task.ID, AuthorUserID: util.MustParseUUID(coauthor)})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, `DELETE FROM chat_message WHERE id=$1`, steered.ID)
	w = httptest.NewRecorder()
	testHandler.ListChatMessages(w, sharedChatRequest(t, coauthor, session, "GET", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Sender == nil || messages[1].Sender.UserID != coauthor {
		t.Fatalf("steered message incorrectly inherited task initiator: %+v", messages)
	}
	if !testHandler.CanReadChatSession(ctx, peer, testWorkspaceID, session) {
		t.Fatal("WS scope should admit participant")
	}
	frame := []byte(fmt.Sprintf(`{"type":"chat:message","payload":{"chat_session_id":%q}}`, session))
	if !testHandler.CanDeliverChatEvent(ctx, peer, testWorkspaceID, frame) {
		t.Fatal("workspace fanout should admit participant")
	}
	frames := [][]byte{
		[]byte(fmt.Sprintf(`{"type":"task:created","payload":{"id":%q,"context":{"content":"full task input"}}}`, sent.TaskID)),
		[]byte(fmt.Sprintf(`{"type":"task:progress","payload":{"task_id":%q,"status":"running"}}`, sent.TaskID)),
		[]byte(fmt.Sprintf(`{"type":"task:message","payload":{"task_id":%q,"message":{"type":"model_delta","content":"private model text"}}}`, sent.TaskID)),
	}
	for _, frame := range frames {
		if !testHandler.CanDeliverChatEvent(ctx, peer, testWorkspaceID, frame) {
			t.Fatalf("accessible task frame denied: %s", frame)
		}
	}
	// Revocation affects existing links, every new send, and already-connected
	// realtime delivery. It never grants the original creator's authority.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='private' WHERE id=$1`, agent)
	for _, role := range []string{"member", "admin", "owner"} {
		dbfx.Exec(t, `UPDATE member SET role=$1 WHERE workspace_id=$2 AND user_id=$3`, role, testWorkspaceID, peer)
		for _, method := range []string{"GET", "POST"} {
			w = httptest.NewRecorder()
			r := sharedChatRequest(t, peer, session, method, map[string]any{"content": "must not execute"})
			if method == "GET" {
				testHandler.GetChatSession(w, r)
			} else {
				testHandler.SendChatMessage(w, r)
			}
			if w.Code != 403 {
				t.Fatalf("private %s %s = %d", role, method, w.Code)
			}
		}
		if testHandler.CanDeliverChatEvent(ctx, peer, testWorkspaceID, frame) {
			t.Fatalf("revoked %s still received frame", role)
		}
		for _, frame := range frames {
			if testHandler.CanDeliverChatEvent(ctx, peer, testWorkspaceID, frame) {
				t.Fatalf("revoked %s received full task/status/model frame: %s", role, frame)
			}
		}
		if testHandler.CanDeliverChatEvent(ctx, peer, testWorkspaceID, []byte(`{"type":"task:created","payload":{"context":{"content":"unmapped secret"}}}`)) {
			t.Fatal("unmapped content-bearing task event must fail closed")
		}
	}
	// An explicit member grant opens the same historical chat without changing
	// its owner. The agent owner's identity is never substituted.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, agent)
	dbfx.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id=$1`, agent)
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": agent, "target_type": "member", "target_id": peer})
	if !testHandler.CanReadChatSession(ctx, peer, testWorkspaceID, session) {
		t.Fatal("explicit member grant not honored")
	}
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, peer)
	if testHandler.CanReadChatSession(ctx, peer, testWorkspaceID, session) {
		t.Fatal("removed member remains readable")
	}
}

func TestSharedChatOwnerOnlyMutationsAndSpoofing(t *testing.T) {
	if testHandler == nil {
		t.Skip("test database unavailable")
	}
	_, session, peer := sharedChatFixture(t)
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		body    any
	}{
		"rename":  {testHandler.UpdateChatSession, map[string]any{"title": "not yours"}},
		"pin":     {testHandler.SetChatSessionPinned, map[string]any{"pinned": true}},
		"archive": {testHandler.SetChatSessionArchived, map[string]any{"archived": true}},
		"delete":  {testHandler.DeleteChatSession, nil},
		"clear":   {testHandler.ClearQueuedChatTasks, nil},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, sharedChatRequest(t, peer, session, "PATCH", tc.body))
			if w.Code != 403 {
				t.Fatalf("nonowner mutation = %d %s", w.Code, w.Body.String())
			}
		})
	}
	outsider := dbfx.User(t, "Outside", "chat-outsider@multica.test")
	w := httptest.NewRecorder()
	r := sharedChatRequest(t, outsider, session, "GET", nil)
	r.Header.Set("X-Agent-ID", "spoofed")
	r.Header.Set("X-Actor-Source", "agent")
	testHandler.GetChatSession(w, r)
	if w.Code != 403 {
		t.Fatalf("spoofed member context admitted outsider: %d", w.Code)
	}
	other := dbfx.Workspace(t, "Other", "chat-access-other")
	dbfx.Member(t, other, peer, "member")
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: util.MustParseUUID(peer), WorkspaceID: util.MustParseUUID(other)})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r = sharedChatRequest(t, peer, session, "GET", nil)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), other, member))
	testHandler.GetChatSession(w, r)
	if w.Code != 404 {
		t.Fatalf("crossworkspace = %d", w.Code)
	}
}

func TestSharedChatAttachmentAndToolLogReadsRecheckAgentACL(t *testing.T) {
	if testHandler == nil {
		t.Skip("test database unavailable")
	}
	agent, session, peer := sharedChatFixture(t)
	task := dbfx.Task(t, agent, testutil.Cols{"chat_session_id": session, "status": "completed", "completed_at": testutil.Raw("now()")})
	attachment := dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id": testWorkspaceID, "chat_session_id": session,
		"uploader_type": "member", "uploader_id": testUserID,
		"filename": "result.txt", "url": "https://files.example.test/result.txt",
		"content_type": "text/plain", "size_bytes": 4,
	})
	readAttachment := func(t *testing.T) int {
		t.Helper()
		w := httptest.NewRecorder()
		r := withURLParam(sharedChatRequest(t, peer, session, "GET", nil), "id", attachment)
		if _, ok := testHandler.loadAttachmentForRequest(w, r); ok {
			return 200
		}
		return w.Code
	}
	readDownload := func(t *testing.T) int {
		t.Helper()
		w := httptest.NewRecorder()
		r := withURLParam(sharedChatRequest(t, peer, session, "GET", nil), "id", attachment)
		if _, ok := testHandler.loadAttachmentForDownload(w, r); ok {
			return 200
		}
		return w.Code
	}
	readLog := func(t *testing.T) int {
		t.Helper()
		w := httptest.NewRecorder()
		r := withURLParam(sharedChatRequest(t, peer, session, "GET", nil), "taskId", task)
		testHandler.ListTaskMessagesByUser(w, r)
		return w.Code
	}
	for name, read := range map[string]func(*testing.T) int{"attachment metadata/content gate": readAttachment, "self-resolving download": readDownload, "tool logs": readLog} {
		t.Run(name, func(t *testing.T) {
			if status := read(t); status != 200 {
				t.Fatalf("accessible participant read = %d", status)
			}
		})
	}
	dbfx.Exec(t, `UPDATE agent SET permission_mode='private' WHERE id=$1`, agent)
	if readAttachment(t) != 404 || readDownload(t) != 404 || readLog(t) != 403 {
		t.Fatal("ACL revocation did not protect attachment/log reads")
	}
	// Membership cached by an earlier download must not keep chat content open
	// after removal; the chat guard reads current membership itself.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, agent)
	dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, peer)
	if readDownload(t) != 404 || readLog(t) != 403 {
		t.Fatal("workspace removal did not protect attachment/log reads")
	}
}
