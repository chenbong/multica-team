package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestInfoFlowPrivateIMBindingPublicMetadata(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation.Kind, f.req.Conversation.ID = "private", f.req.Sender
	f.req.RobotChatID, f.req.RobotName = "9007199254740993", "  Public test robot  "
	bound := f.bind(t)
	view := infoFlowConversationCall(t, f, http.MethodGet, f.owner, "", nil, 200)
	if len(view.Private.Bindings) != 1 || view.Private.Bindings[0].RobotName != "Public test robot" || view.Private.Bindings[0].RobotChatID != f.req.RobotChatID || view.Private.Bindings[0].State != "active" {
		t.Fatalf("IM binding lost public display metadata: %+v", view.Private.Bindings)
	}
	// Metadata refresh is not a routing change and does not invalidate output.
	refresh := f.req
	refresh.MessageID, refresh.RobotName = uuid.NewString(), "Renamed public robot"
	if got := infoFlowCall(t, f.h, refresh, 200); got.Binding.Generation != bound.Binding.Generation {
		t.Fatal("public display metadata rotated the binding generation")
	}
	// Legacy prepared inputs omit metadata and must retain previously verified
	// fields rather than clear them or change their receipt hash.
	legacy := f.req
	legacy.MessageID, legacy.RobotName, legacy.RobotChatID = uuid.NewString(), "", ""
	infoFlowCall(t, f.h, legacy, 200)
	infoFlowCall(t, f.h, legacy, 200)
	view = infoFlowConversationCall(t, f, http.MethodGet, f.owner, "", nil, 200)
	if view.Private.Bindings[0].RobotName != refresh.RobotName || view.Private.Bindings[0].RobotChatID != f.req.RobotChatID {
		t.Fatal("legacy bind cleared verified public metadata")
	}
	// A repeated message cannot substitute a different SDK snapshot.
	refresh.RobotName = "Changed retry snapshot"
	infoFlowCall(t, f.h, refresh, 409)
	other := infoFlowConversationCall(t, f, http.MethodGet, f.user, "", nil, 200)
	if len(other.Private.Bindings) != 0 {
		t.Fatal("robot display metadata leaked another user's private binding")
	}
}

func TestInfoFlowPrivateMetadataOnlyOnExplicitPrivateBinding(t *testing.T) {
	f := infoFlowConversationFixture(t)
	for _, tc := range []struct{ kind, operation, chat, name string }{
		{"group", "bind", "123", "Robot"},
		{"private", "message", "123", "Robot"},
		{"private", "binding", "123", "Robot"},
		{"private", "unbind", "123", "Robot"},
		{"private", "bind", "0", "Robot"},
		{"private", "bind", "9223372036854775808", "Robot"},
		{"private", "bind", "123", ""},
		{"private", "bind", "", "Robot"},
		{"private", "bind", "123", "Unsafe\tname"},
		{"private", "bind", "123", strings.Repeat("x", 257)},
	} {
		t.Run(tc.kind+"/"+tc.operation+"/"+tc.chat+"/"+tc.name, func(t *testing.T) {
			req := f.req
			req.Conversation.Kind, req.Operation = tc.kind, tc.operation
			if tc.kind == "private" {
				req.Conversation.ID = req.Sender
			}
			req.RobotChatID, req.RobotName = tc.chat, tc.name
			infoFlowCall(t, f.h, req, 400)
		})
	}
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_issue_binding WHERE robot_id=$1`, f.req.RobotID) != 0 {
		t.Fatal("invalid display metadata created a binding")
	}
}

func TestInfoFlowPrivateIssueCreationRetainsRobotMetadata(t *testing.T) {
	f := infoFlowConversationFixture(t)
	f.req.Conversation.Kind, f.req.Conversation.ID = "private", f.req.Sender
	f.req.Operation, f.req.IssueRef, f.req.Content = "create", "", "Private metadata creation fixture"
	f.req.RobotChatID, f.req.RobotName = "123456789", "Private creation robot"
	out := infoFlowCall(t, f.h, f.req, 200)
	if out.Binding == nil || out.Binding.IssueID == "" {
		t.Fatal("private issue creation did not return its binding")
	}
	f.issue = out.Binding.IssueID
	view := infoFlowConversationCall(t, f, http.MethodGet, f.owner, "", nil, 200)
	if len(view.Private.Bindings) != 1 || view.Private.Bindings[0].RobotChatID != f.req.RobotChatID || view.Private.Bindings[0].RobotName != f.req.RobotName {
		t.Fatalf("created private issue lost robot metadata: %+v", view.Private.Bindings)
	}
}
