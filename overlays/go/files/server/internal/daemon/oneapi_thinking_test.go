package daemon

import (
	"context"
	"github.com/multica-ai/multica/server/pkg/agent"
	"testing"
)

func TestOneAPIThinkingTaskSelection(t *testing.T) {
	for _, tc := range []struct {
		provider, command, model string
		keep                     bool
	}{
		{"codex", "/fixture/shims/ducx", "gpt-5.6-luna", true},
		{"claude", "/fixture/shims/ducc", "Opus 5.5", true},
		{"codex", "/fixture/codex", "gpt-5.6-luna", false},
		{"claude", "/fixture/claude", "Opus 5.5", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			reads := stubModelDiscovery(t, map[string]agent.Catalog{tc.provider: {Models: []agent.Model{{ID: "local-default-only"}}}})
			in := taskModelSelection{Model: tc.model, ThinkingLevel: "max"}
			got := resolveTaskModelSelection(context.Background(), tc.provider, agent.NewCommand(tc.command, nil), in, quietTaskLog())
			want := in
			if !tc.keep {
				want.ThinkingLevel = ""
			}
			if got != want || reads() != 1 {
				t.Fatalf("got %+v (reads %d), want %+v", got, reads(), want)
			}
		})
	}
}
