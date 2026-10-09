package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/agent"
)

type fixtureRuntimeCatalog struct {
	calls  []string
	forced []bool
	err    error
}

func (f *fixtureRuntimeCatalog) Models(_ context.Context, protocol string, force bool) (agent.OneAPIModelResult, error) {
	f.calls = append(f.calls, protocol)
	f.forced = append(f.forced, force)
	return agent.OneAPIModelResult{Models: []agent.Model{{ID: protocol + "-fixture", Label: protocol + "-fixture", Provider: protocol}}, StoredAt: time.Now()}, f.err
}

func TestOneAPIRuntimeProtocol(t *testing.T) {
	for _, tc := range []struct{ provider, command, want string }{
		{"claude", "ducc", "messages"}, {"claude", "/root/.baidu-cc/baidu-cc/bin/ducc", "messages"},
		{"codex", "ducx", "responses"}, {"codex", "/root/.baidu-cx/baidu-cx/bin/ducx", "responses"},
		{"codex", "codex", ""}, {"claude", "claude", ""}, {"claude", "ducx", ""}, {"codex", "ducc", ""},
		{"claude", "fake-ducc", ""}, {"claude", "ducc --username alice", ""},
	} {
		if got := oneAPIRuntimeProtocol(tc.provider, tc.command); got != tc.want {
			t.Fatalf("%q/%q: %q", tc.provider, tc.command, got)
		}
	}
	t.Setenv("MULTICA_ONEAPI_CATALOG_URL", "")
	if configuredOneAPIModelCatalog() != nil {
		t.Fatal("disabled feature must be a nil interface")
	}
}

func TestOneAPIRuntimeCatalogEndpoint(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ provider, command, protocol string }{
		{"claude", "ducc", "messages"}, {"codex", "ducx", "responses"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			profile := dbfx.Insert(t, "runtime_profile", testutil.Cols{"workspace_id": testWorkspaceID, "display_name": "OneAPI " + tc.command, "protocol_family": tc.provider, "command_name": tc.command, "created_by": testUserID})
			runtimeID := dbfx.Runtime(t, "OneAPI fixture", testutil.Cols{"provider": tc.provider, "profile_id": profile})
			agentID := dbfx.Agent(t, "Keep existing model", runtimeID, testutil.Cols{"model": "keep-current[1m]"})
			h := *testHandler
			catalog := &fixtureRuntimeCatalog{}
			h.OneAPIModelCatalog = catalog
			h.ModelListStore = NewInMemoryModelListStore()
			h.ModelCatalogCache = NewInMemoryModelCatalogCache()
			if err := h.ModelCatalogCache.Put(ctx, runtimeID, []ModelEntry{{ID: "old-daemon-default"}}, nil, true); err != nil {
				t.Fatal(err)
			}
			request := func(query string) *http.Request {
				return withURLParam(newRequest("POST", "/api/runtimes/"+runtimeID+"/models"+query, nil), "runtimeId", runtimeID)
			}
			var got ModelListRequest
			testutil.Call(t, h.InitiateListModels, request("")).Want(200).JSON(&got)
			if got.Status != ModelListCompleted || !got.Supported || len(got.Models) != 1 || got.Models[0].ID != tc.protocol+"-fixture" {
				t.Fatalf("wrong catalog: %+v", got)
			}
			testutil.Call(t, h.InitiateListModels, request("?force=true")).Want(200).JSON(&got)
			if len(catalog.calls) != 2 || catalog.calls[0] != tc.protocol || !catalog.forced[1] {
				t.Fatal("refresh ignored")
			}
			if pending, _ := h.ModelListStore.HasPending(ctx, runtimeID); pending {
				t.Fatal("OneAPI selection queued daemon work")
			}
			if snapshot, _ := h.ModelCatalogCache.Get(ctx, runtimeID); snapshot.Models[0].ID != "old-daemon-default" {
				t.Fatal("gateway names replaced CLI capability evidence")
			}
			catalog.err = errors.New("catalog temporarily unavailable")
			testutil.Call(t, h.InitiateListModels, request("?force=true")).Want(200).JSON(&got)
			if got.Status != ModelListFailed || !got.Supported {
				t.Fatal("failure silently disabled selection")
			}
			var model string
			if err := testPool.QueryRow(ctx, "SELECT model FROM agent WHERE id=$1", agentID).Scan(&model); err != nil || model != "keep-current[1m]" {
				t.Fatal("discovery changed an agent model")
			}
			other := dbfx.User(t, "OneAPI outsider", tc.command+"-catalog-outsider@example.test")
			denied := request("")
			denied.Header.Set("X-User-ID", other)
			testutil.Call(t, h.InitiateListModels, denied).Want(404)
			dbfx.Member(t, testWorkspaceID, other, "member")
			testutil.Call(t, h.InitiateListModels, denied).Want(404)
			if len(catalog.calls) != 3 {
				t.Fatal("unauthorized caller reached shared credential")
			}
			dbfx.Exec(t, "UPDATE agent_runtime SET status='offline' WHERE id=$1", runtimeID)
			testutil.Call(t, h.InitiateListModels, request("")).Want(503)
			if len(catalog.calls) != 3 {
				t.Fatal("offline runtime contacted catalog")
			}
		})
	}
}

func TestOneAPICatalogLeavesOtherRuntimesUnchanged(t *testing.T) {
	for _, command := range []string{"", "codex", "my-wrapper"} {
		t.Run(command, func(t *testing.T) {
			cols := testutil.Cols{"provider": "codex"}
			if command != "" {
				cols["profile_id"] = dbfx.Insert(t, "runtime_profile", testutil.Cols{"workspace_id": testWorkspaceID, "display_name": "Other " + command, "protocol_family": "codex", "command_name": command})
			}
			runtimeID := dbfx.Runtime(t, "Other runtime", cols)
			h := *testHandler
			catalog := &fixtureRuntimeCatalog{}
			h.OneAPIModelCatalog = catalog
			h.ModelListStore = NewInMemoryModelListStore()
			h.ModelCatalogCache = NewInMemoryModelCatalogCache()
			req := withURLParam(newRequest("POST", "/models", nil), "runtimeId", runtimeID)
			var got ModelListRequest
			testutil.Call(t, h.InitiateListModels, req).Want(200).JSON(&got)
			if got.Status != ModelListPending || len(catalog.calls) != 0 {
				t.Fatal("ordinary runtime was intercepted")
			}
		})
	}
}
