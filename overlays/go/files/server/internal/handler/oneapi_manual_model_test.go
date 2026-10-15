package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestOneAPIManualModelThinkingUpdate(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, "manual-model-fixture", testutil.Cols{"provider": provider})
			agentID := dbfx.Agent(t, "manual-model-agent", runtimeID, testutil.Cols{"owner_id": testUserID})
			w := httptest.NewRecorder()
			r := withURLParams(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{"model": "DD", "thinking_level": "high"}), "id", agentID)
			testHandler.UpdateAgent(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("manual model effort update: HTTP %d", w.Code)
			}
			var model, level string
			if err := testPool.QueryRow(context.Background(), `SELECT model,thinking_level FROM agent WHERE id=$1`, agentID).Scan(&model, &level); err != nil || model != "DD" || level != "high" {
				t.Fatal("manual model ID or selected effort was not persisted")
			}
		})
	}
}
