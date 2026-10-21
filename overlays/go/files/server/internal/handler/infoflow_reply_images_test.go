package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func infoFlowSeedReplyImage(t *testing.T, f infoFlowTestFixture, comment string, uploader string, size int64) string {
	t.Helper()
	return dbfx.Insert(t, "attachment", testutil.Cols{
		"workspace_id": f.req.WorkspaceID, "issue_id": f.issue, "comment_id": comment,
		"uploader_type": uploader, "uploader_id": f.req.AgentID, "filename": "fixture.png",
		"url": "https://cdn.example.com/reply-fixture", "content_type": "image/png", "size_bytes": size,
	})
}

func infoFlowReplyImageCall(t *testing.T, f infoFlowTestFixture, delivery, image, token string, status int) *testutil.Response {
	t.Helper()
	r := testutil.WithURLParams(testutil.WithHeaders(testutil.JSONRequest("POST", "/reply-image", f.req.infoFlowScope),
		"Authorization", "Bearer "+token), "id", delivery, "attachmentId", image)
	return testutil.Call(t, f.h.InfoFlowIssueReplyImage, r).Want(status)
}

func TestInfoFlowReplyImagesMetadataAndScopedDownload(t *testing.T) {
	for _, kind := range []string{"group", "private"} {
		t.Run(kind, func(t *testing.T) {
			f := newInfoFlowNotifyFixture(t)
			if kind == "private" {
				f.req.Conversation = infoFlowConversation{Kind: "private", ID: f.req.Sender}
			}
			f.bind(t)
			comment := infoFlowAgentReply(t, f, f.issue, "", "", "Generated image")
			body := []byte("public reply image fixture")
			image := infoFlowSeedReplyImage(t, f, comment, "agent", int64(len(body)))
			store := &mockStorage{}
			_, err := store.Upload(context.Background(), "reply-fixture", body, "image/png", "fixture.png")
			if err != nil {
				t.Fatal(err)
			}
			f.h.Storage = store
			rows := infoFlowPoll(t, f, 20)
			if len(rows) != 1 || len(rows[0].ImageAttachments) != 1 || rows[0].ImageCount != 1 || rows[0].CommentCreatedAt == "" {
				t.Fatalf("missing scoped metadata: %+v", rows)
			}
			if rows[0].ImageAttachments[0].ID != image || rows[0].ImageAttachments[0].UploaderType != "agent" ||
				!strings.HasSuffix(rows[0].Content, rows[0].ImageNotice) {
				t.Fatal("image notice or attachment ownership changed")
			}
			response := infoFlowReplyImageCall(t, f, rows[0].ID, image, infoFlowTestToken, 200)
			if response.Text() != string(body) || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("image bytes or private response headers changed")
			}
			infoFlowReplyImageCall(t, f, rows[0].ID, image, "invalid", 401)
			infoFlowReplyImageCall(t, f, "bad-uuid", image, infoFlowTestToken, 400)
			infoFlowReplyImageCall(t, f, rows[0].ID, "bad-uuid", infoFlowTestToken, 400)
			foreign := infoFlowAgentReply(t, f, f.issue, "", "", "Another reply")
			foreignImage := infoFlowSeedReplyImage(t, f, foreign, "agent", 1)
			infoFlowReplyImageCall(t, f, rows[0].ID, foreignImage, infoFlowTestToken, 404)
			other := f
			other.req.WorkspaceID = uuid.NewString()
			infoFlowReplyImageCall(t, other, rows[0].ID, image, infoFlowTestToken, 404)
			infoFlowSyncCall(t, f, f.owner, rows[0].Binding.BindingID, rows[0].Binding.Generation, false, 200)
			infoFlowReplyImageCall(t, f, rows[0].ID, image, infoFlowTestToken, 404)
			if store.getReaderCalls != 1 {
				t.Fatal("denied image request reached object storage")
			}
		})
	}
}

func TestInfoFlowReplyImagesRejectHumanAttachmentsAndOversize(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	comment := infoFlowAgentReply(t, f, f.issue, "", "", "Agent comment with reused human picture")
	image := infoFlowSeedReplyImage(t, f, comment, "member", 20)
	f.h.Storage = &mockStorage{}
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 || len(rows[0].ImageAttachments) != 0 {
		t.Fatal("human picture was advertised as generated output")
	}
	infoFlowReplyImageCall(t, f, rows[0].ID, image, infoFlowTestToken, 404)
	large := infoFlowSeedReplyImage(t, f, comment, "agent", 20*1024*1024+1)
	infoFlowReplyImageCall(t, f, rows[0].ID, large, infoFlowTestToken, http.StatusRequestEntityTooLarge)
	dbfx.Exec(t, `UPDATE comment SET deleted_at=clock_timestamp() WHERE id=$1`, comment)
	infoFlowReplyImageCall(t, f, rows[0].ID, large, infoFlowTestToken, 404)
}

func TestInfoFlowReplyImagesAckedDeliveryCannotDownloadAgain(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	comment := infoFlowAgentReply(t, f, f.issue, "", "", "Generated image")
	image := infoFlowSeedReplyImage(t, f, comment, "agent", 1)
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 {
		t.Fatal("missing delivery")
	}
	infoFlowAck(t, f, rows[0].ID, nil, 200)
	f.h.Storage = &mockStorage{}
	infoFlowReplyImageCall(t, f, rows[0].ID, image, infoFlowTestToken, 404)
}
