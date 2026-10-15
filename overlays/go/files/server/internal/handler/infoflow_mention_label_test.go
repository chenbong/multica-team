package handler

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestInfoFlowNamedMentionPreservesTargetRole(t *testing.T) {
	const target = "11111111-1111-4111-8111-111111111111"
	for _, kind := range []string{"agent", "squad"} {
		got := infoFlowNamedMention(kind, target, "GB-16-RSI")
		if got != "[@GB-16-RSI](mention://"+kind+"/"+target+")" {
			t.Fatal(got)
		}
		mentions := util.ParseMentions(got)
		if len(mentions) != 1 || mentions[0].Type != kind || mentions[0].ID != target {
			t.Fatalf("dispatch changed: %+v", mentions)
		}
	}
}

func TestInfoFlowNamedMentionCannotInjectAnotherTarget(t *testing.T) {
	const target = "11111111-1111-4111-8111-111111111111"
	for _, name := range []string{
		"Worker [A](gpu)", "Worker\\A", "Worker\nA\r\t\x00", "Worker\u2028A",
		"evil](mention://agent/22222222-2222-4222-8222-222222222222) [@extra",
		"[@other](mention://squad/33333333-3333-4333-8333-333333333333)", "",
	} {
		got := infoFlowNamedMention("agent", target, name)
		if strings.ContainsAny(got, "\n\r\x00") {
			t.Fatal("label broke the comment boundary")
		}
		mentions := util.ParseMentions(got)
		if len(mentions) != 1 || mentions[0].Type != "agent" || mentions[0].ID != target {
			t.Fatalf("name injected a target: %q: %+v", name, mentions)
		}
	}
}
