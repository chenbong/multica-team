package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type infoFlowSecondImageFails struct {
	mockStorage
	calls int
}

func (s *infoFlowSecondImageFails) Upload(ctx context.Context, key string, data []byte, contentType, filename string) (string, error) {
	s.calls++
	if s.calls == 2 {
		return "", errors.New("temporary storage outage")
	}
	return s.mockStorage.Upload(ctx, key, data, contentType, filename)
}

func (s *infoFlowSecondImageFails) UploadStream(ctx context.Context, key string, reader io.Reader, size int64, contentType, filename string) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return s.Upload(ctx, key, data, contentType, filename)
}

func TestInfoFlowImageBatchUploadRollbackAndRetry(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	f.h.Storage = &infoFlowSecondImageFails{}
	r := f.req
	r.Operation, r.MessageID, r.Content = "message", uuid.NewString(), "Two images"
	infoFlowPin(t, &r)
	for _, name := range []string{"first.png", "second.png"} {
		r.Attachments = append(r.Attachments, infoFlowAttachment{Filename: name, DataBase64: base64.StdEncoding.EncodeToString([]byte("fixture image"))})
	}
	infoFlowCall(t, f.h, r, 503)
	for _, table := range []string{"comment", "attachment", "agent_task_queue"} {
		if infoFlowCount(t, `SELECT count(*) FROM `+table+` WHERE issue_id=$1`, f.issue) != 0 {
			t.Fatalf("partial image batch wrote %s", table)
		}
	}
	accepted := infoFlowCall(t, f.h, r, 200)
	if accepted.CommentID == "" {
		t.Fatal("retry did not commit complete input")
	}
	infoFlowCall(t, f.h, r, 200)
	if infoFlowCount(t, `SELECT count(*) FROM attachment WHERE comment_id=$1`, accepted.CommentID) != 2 {
		t.Fatal("retry lost or duplicated attachments")
	}
}

func TestInfoFlowImageQuoteRequiresOwnConversationRobotOutput(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	infoFlowAgentReply(t, f, f.issue, "", "", "Robot output")
	deliveries := infoFlowPoll(t, f, 20)
	if len(deliveries) != 1 {
		t.Fatal("expected robot output")
	}
	infoFlowAck(t, f, deliveries[0].ID, []string{"900001"}, 200)
	r := f.req
	r.Operation, r.MessageID, r.Content = "message", uuid.NewString(), "Analyze the next image"
	r.RequireRobotQuote, r.ReplyToMessageID = true, "900001"
	infoFlowPin(t, &r)
	accepted := infoFlowCall(t, f.h, r, 200)
	if accepted.CommentID == "" {
		t.Fatal("verified robot quote was rejected")
	}
	before := infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue)
	r.ReplyToMessageID, r.MessageID = r.MessageID, uuid.NewString()
	ignored := infoFlowCall(t, f.h, r, 200)
	if !ignored.Silent || ignored.CommentID != "" {
		t.Fatal("human quote was treated as robot request")
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != before {
		t.Fatal("ignored input created a comment")
	}
	r.ReplyToMessageID, r.MessageID, r.Conversation.ID = "900001", uuid.NewString(), "900099"
	if out := infoFlowCall(t, f.h, r, 200); !out.Silent || out.CommentID != "" {
		t.Fatal("foreign conversation quote was accepted")
	}
}

func TestInfoFlowImageSubmissionReceiptIsReadOnlyAndDurable(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	r := f.req
	r.Operation, r.MessageID, r.Content = "binding", uuid.NewString(), "Picture input"
	infoFlowPin(t, &r)
	before := infoFlowCall(t, f.h, r, 200)
	if before.InputReceived == nil || *before.InputReceived {
		t.Fatal("missing explicit negative receipt")
	}
	r.Operation = "message"
	posted := infoFlowCall(t, f.h, r, 200)
	if posted.CommentID == "" {
		t.Fatal("input was not stored")
	}
	r.Operation = "binding"
	after := infoFlowCall(t, f.h, r, 200)
	if after.InputReceived == nil || !*after.InputReceived {
		t.Fatal("durable receipt was not returned")
	}
	if infoFlowCount(t, `SELECT count(*) FROM comment WHERE issue_id=$1`, f.issue) != 1 {
		t.Fatal("receipt check wrote another comment")
	}
}

func TestInfoFlowImageSummarySnapshotDoesNotExposeAttachmentURLs(t *testing.T) {
	f := newInfoFlowNotifyFixture(t)
	f.bind(t)
	c := dbfx.Comment(t, f.issue, "Two diagrams", testutil.Cols{"author_id": f.owner})
	for _, name := range []string{"first.png", "second.png"} {
		dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": f.req.WorkspaceID, "issue_id": f.issue,
			"comment_id": c, "uploader_type": "member", "uploader_id": f.owner, "filename": name,
			"url": "https://example.test/private-image", "content_type": "image/png", "size_bytes": 3})
	}
	rows := infoFlowPoll(t, f, 20)
	if len(rows) != 1 || !strings.Contains(rows[0].Content, "图片：2 张") || strings.Contains(rows[0].Content, "private-image") {
		t.Fatalf("invalid image summary: %+v", rows)
	}
	dbfx.Exec(t, `DELETE FROM attachment WHERE comment_id=$1`, c)
	again := infoFlowPoll(t, f, 20)
	if len(again) != 1 || again[0].Content != rows[0].Content {
		t.Fatal("pending image summary changed on retry")
	}
}
