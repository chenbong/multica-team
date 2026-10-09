package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"
)

func TestOneAPIThinkingPicker(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		got := OneAPIThinking(provider)
		var values []string
		for _, level := range got.SupportedLevels {
			values = append(values, level.Value)
			if level.Label == "" {
				t.Fatal("missing native label")
			}
		}
		if !reflect.DeepEqual(values, []string{"low", "medium", "high", "xhigh", "max"}) || got.DefaultLevel != "" {
			t.Fatalf("unexpected picker: %+v", got)
		}
		got.SupportedLevels[0].Value = "mutated"
		if OneAPIThinking(provider).SupportedLevels[0].Value != "low" {
			t.Fatal("shared mutable capability")
		}
	}
	if OneAPIThinking("other") != nil {
		t.Fatal("other provider affected")
	}
}

func TestOneAPIThinkingExecutionGate(t *testing.T) {
	for _, tc := range []struct {
		provider, command, model, level string
		want                            bool
	}{
		{"codex", "/fixture/shims/ducx", "gpt-5.6-luna", "max", true},
		{"claude", "/fixture/shims/ducc", "Opus 5.5", "max", true},
		{"codex", "ducx", "new-oneapi-model", "high", true},
		{"claude", "ducc", "new-oneapi-model", "xhigh", true},
		{"codex", "codex", "gpt-5.6-luna", "max", false},
		{"claude", "claude", "Opus 5.5", "max", false},
		{"codex", "not-ducx", "new-oneapi-model", "max", false},
		{"claude", "ducx", "new-oneapi-model", "max", false},
		{"codex", "ducx", "", "max", false},
		{"claude", "ducc", "Opus 5.5", "ultra", false},
		{"codex", "ducx", "model", "arbitrary-token", false},
		{"codex", "ducx", "model", "", true},
	} {
		t.Run(tc.provider+tc.command+tc.model+tc.level, func(t *testing.T) {
			load := func() (Catalog, error) { return Catalog{Models: []Model{{ID: "local-default-only"}}}, nil }
			ok, err := ValidateRuntimeThinkingLevelWith(load, tc.provider, NewCommand(tc.command, nil), tc.model, tc.level)
			if err != nil || ok != tc.want {
				t.Fatalf("got %v/%v, want %v", ok, err, tc.want)
			}
		})
	}
	for _, levels := range [][]string{{}, {"low", "high"}} {
		load := func() (Catalog, error) { return Catalog{CLIThinkingLevels: levels}, nil }
		if ok, err := ValidateRuntimeThinkingLevelWith(load, "claude", NewCommand("ducc", nil), "Opus 5.5", "max"); ok || err != nil {
			t.Fatal("ignored CLI flag support")
		}
	}
	errFixture := errors.New("fixture discovery failure")
	load := func() (Catalog, error) { return Catalog{}, errFixture }
	if _, err := ValidateRuntimeThinkingLevelWith(load, "codex", NewCommand("ducx", nil), "model", "max"); err != errFixture {
		t.Fatal("discovery error hidden")
	}
}

func TestOneAPIThinkingClaudeArguments(t *testing.T) {
	args := buildClaudeArgs(ExecOptions{Model: "Opus 5.5", ThinkingLevel: "max"}, slog.Default())
	index := slices.Index(args, "--effort")
	if index < 0 || index+1 >= len(args) || args[index+1] != "max" {
		t.Fatalf("effort not forwarded: %v", args)
	}
}

func TestOneAPIThinkingCodexRPC(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			c, fs, _ := newTestCodexClient(t)
			c.cfg.ExecutablePath = "/fixture/shims/ducx"
			method := "thread/start"
			opts := ExecOptions{Cwd: "/work", Model: "gpt-5.6-luna", ThinkingLevel: "max"}
			if resume {
				method = "thread/resume"
				opts.ResumeSessionID = "previous"
			}
			wait := drainRPCScript(t, c, fs, []rpcResponse{{method: method, result: json.RawMessage(`{"thread":{"id":"fixture-thread"}}`), assertFn: func(t *testing.T, p map[string]any) {
				cfg, _ := p["config"].(map[string]any)
				if p["model"] != "gpt-5.6-luna" || p["modelProvider"] != "oneapi" || cfg["model_reasoning_effort"] != "max" {
					t.Fatalf("missing model/effort override: %+v", p)
				}
			}}})
			defer wait()
			if _, _, err := c.startOrResumeThread(context.Background(), opts, slog.Default()); err != nil {
				t.Fatal(err)
			}
			turn := map[string]any{"input": []any{}}
			applyCodexReasoningEffort(turn, opts.ThinkingLevel)
			if turn["effort"] != "max" {
				t.Fatal("turn/start dropped effort")
			}
		})
	}
}
