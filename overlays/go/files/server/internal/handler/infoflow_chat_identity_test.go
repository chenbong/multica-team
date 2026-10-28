package handler

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http"
	"strings"
	"testing"
)

func infoFlowChatCall(t *testing.T, f infoFlowTestFixture, body map[string]any, status int) map[string]any {
	t.Helper()
	body["robot_id"] = f.req.RobotID
	body["agent_id"] = f.req.AgentID
	body["workspace_id"] = f.req.WorkspaceID
	req := newRequest(http.MethodPost, "/api/integrations/infoflow/chat-input", body)
	req.Header.Set("Authorization", "Bearer "+infoFlowTestToken)
	// Neither a forged owner nor a stale task header may become the sender.
	req.Header.Set("X-User-ID", f.owner)
	req.Header.Set("X-Agent-ID", f.req.AgentID)
	req.Header.Set("X-Task-ID", uuid.NewString())
	result := testutil.Call(t, f.h.InfoFlowChatInput, req).Want(status)
	var out map[string]any
	if status < 300 {
		if err := json.Unmarshal(result.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func TestInfoFlowChatInputUsesRealSenderForChatAndIssueCreation(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowFixture(t)
			var email string
			dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
			sender := strings.Split(email, "@")[0]
			dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to',visibility='workspace' WHERE id=$1`, f.req.AgentID)
			dbfx.Exec(t, `INSERT INTO agent_invocation_target(agent_id,target_type,target_id) VALUES($1,'workspace',$2) ON CONFLICT DO NOTHING`, f.req.AgentID, testWorkspaceID)
			conversation := infoFlowConversation{Kind: kind, ID: f.req.Conversation.ID}
			if kind == "private" {
				conversation.ID = sender
			}
			session := dbfx.Insert(t, "chat_session", testutil.Cols{"workspace_id": testWorkspaceID, "agent_id": f.req.AgentID, "creator_id": f.owner, "title": "Existing owner-profile thread", "status": "active", "explicitly_created_at": testutil.Raw("now()")})
			result := infoFlowChatCall(t, f, map[string]any{"sender": sender, "conversation": conversation, "session_id": session, "content": "Real sender message", "attachment_ids": []string{}}, 201)
			dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE chat_session_id=$1`, session)
			taskID, ok := result["task_id"].(string)
			if !ok || taskID == "" {
				t.Fatal("chat task was not accepted")
			}
			dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, taskID)
			var author, originator string
			dbfx.QueryRow(t, `SELECT author_user_id FROM chat_message WHERE chat_session_id=$1 AND role='user' ORDER BY created_at DESC LIMIT 1`, session).Scan(&author)
			dbfx.QueryRow(t, `SELECT originator_user_id FROM agent_task_queue WHERE id=$1`, taskID).Scan(&originator)
			if author != f.user || originator != f.user {
				t.Fatalf("owner profile substituted for sender: author=%s originator=%s", author, originator)
			}
			image := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "chat_session_id": session, "uploader_type": "member", "uploader_id": f.owner, "filename": "incoming.png", "url": "/uploads/workspaces/fixture/incoming.png", "content_type": "image/png", "size_bytes": 1})
			imageReply := infoFlowChatCall(t, f, map[string]any{"sender": sender, "conversation": conversation, "session_id": session, "content": "Incoming image from real requester", "attachment_ids": []string{image}}, 201)
			if n := dbfx.Count(t, `SELECT count(*) FROM attachment a JOIN chat_message m ON m.id=a.chat_message_id WHERE a.id=$1 AND m.author_user_id=$2 AND m.id=$3`, image, f.user, imageReply["message_id"]); n != 1 {
				t.Fatal("trusted owner-staged image did not bind to the real sender's message")
			}
			// Keep the historical session identity unchanged; only actual message and run authorship changes.
			if n := dbfx.Count(t, `SELECT count(*) FROM chat_session WHERE id=$1 AND creator_id=$2`, session, f.owner); n != 1 {
				t.Fatal("existing chat history identity changed")
			}
			issueImage := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": f.owner, "filename": "issue.png", "url": "/uploads/workspaces/fixture/issue.png", "content_type": "image/png", "size_bytes": 1})
			dbfx.Exec(t, `UPDATE workspace SET issue_counter=greatest(issue_counter,(SELECT coalesce(max(number),0) FROM issue WHERE workspace_id=$1)) WHERE id=$1`, testWorkspaceID)
			created := infoFlowChatCall(t, f, map[string]any{"sender": sender, "conversation": conversation, "create_issue": true, "title": "Requested independent fixture", "content": "Create this for the actual requester", "attachment_ids": []string{issueImage}}, 201)
			issue, ok := created["id"].(string)
			if !ok {
				t.Fatal("created issue id missing")
			}
			dbfx.Cleanup(t, `DELETE FROM issue WHERE id=$1`, issue)
			dbfx.Cleanup(t, `DELETE FROM issue_manager_state WHERE issue_id=$1`, issue)
			dbfx.Cleanup(t, `DELETE FROM issue_manager WHERE issue_id=$1`, issue)
			dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, issue)
			if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND creator_type='member' AND creator_id=$2`, issue, f.user); n != 1 {
				t.Fatal("independent issue has robot-owner creator")
			}
			// The creator retains deletion permission as an ordinary member;
			// retired administrator records must not be recreated for new issues.
			dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
			wantDeletionPermission(t, issue, f.user, "", true)
			if n := dbfx.Count(t, `SELECT count(*) FROM issue_manager WHERE issue_id=$1`, issue); n != 0 {
				t.Fatal("new issue recreated retired task administrators")
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND issue_id=$2`, issueImage, issue); n != 1 {
				t.Fatal("independent issue image was not retained")
			}
			if _, err := testPool.Exec(context.Background(), `DELETE FROM member WHERE id=$1`, f.member); err != nil {
				t.Fatal(err)
			}
			infoFlowChatCall(t, f, map[string]any{"sender": sender, "conversation": conversation, "session_id": session, "content": "Must not be accepted after leaving", "attachment_ids": []string{}}, 403)
		})
	}
}
func TestInfoFlowChatInputDeniesPrivateAgentAndUntrustedCredentials(t *testing.T) {
	f := newInfoFlowFixture(t)
	var email string
	dbfx.QueryRow(t, `SELECT email FROM "user" WHERE id=$1`, f.user).Scan(&email)
	sender := strings.Split(email, "@")[0]
	session := dbfx.Insert(t, "chat_session", testutil.Cols{"workspace_id": testWorkspaceID, "agent_id": f.req.AgentID, "creator_id": f.owner, "title": "Private agent thread", "status": "active", "explicitly_created_at": testutil.Raw("now()")})
	infoFlowChatCall(t, f, map[string]any{"sender": sender, "conversation": f.req.Conversation, "session_id": session, "content": "Workspace admin cannot bypass invocation", "attachment_ids": []string{}}, 403)
	request := newRequest(http.MethodPost, "/api/integrations/infoflow/chat-input", map[string]any{})
	request.Header.Set("Authorization", "Bearer mul_untrusted_fixture")
	testutil.Call(t, f.h.InfoFlowChatInput, request).Want(401)
	if n := dbfx.Count(t, `SELECT count(*) FROM chat_message WHERE chat_session_id=$1`, session); n != 0 {
		t.Fatal("rejected sender created owner-profile messages")
	}
}
