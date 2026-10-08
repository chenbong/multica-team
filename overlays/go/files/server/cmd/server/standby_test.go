package main

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Boot the real router without a database or live channel credentials. Standby
// must preserve its HTTP routes while leaving outbound event delivery unwired.
func TestStandbyRouterDisablesOutboundAndKeepsRoutes(t *testing.T) {
	t.Setenv("LOCAL_UPLOAD_DIR", t.TempDir())
	t.Setenv("S3_BUCKET", "")
	t.Setenv("COMPOSIO_API_KEY", "")
	keys := []string{
		"MULTICA_LARK_SECRET_KEY", "MULTICA_SLACK_SECRET_KEY",
		"MULTICA_DINGTALK_SECRET_KEY", "MULTICA_WECOM_SECRET_KEY",
		"MULTICA_TELEGRAM_SECRET_KEY", "MULTICA_PLUGIN_SECRET_KEY",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	t.Setenv("MULTICA_STANDBY", "true")
	baseline := events.New()
	NewRouter(nil, realtime.NewHub(), baseline, analytics.NoopClient{}, nil)

	fakeKey := base64.StdEncoding.EncodeToString(make([]byte, secretbox.KeySize))
	for _, key := range keys {
		t.Setenv(key, fakeKey)
	}
	standbyBus := events.New()
	router, h := NewRouterWithOptions(nil, realtime.NewHub(), standbyBus, analytics.NoopClient{}, nil, RouterOptions{})
	if h.LarkInstallations == nil || h.SlackInstall == nil || h.TelegramOutbound == nil || h.PluginService == nil {
		t.Fatal("configured integration services must remain available in standby")
	}
	for _, event := range []string{
		protocol.EventChatDone, protocol.EventTaskMessage, protocol.EventTaskQueued,
		protocol.EventTaskFailed, protocol.EventTaskCancelled, protocol.EventInboxNew,
	} {
		if got, want := standbyBus.SubscriberCount(event), baseline.SubscriberCount(event); got != want {
			t.Errorf("standby added outbound %s listeners: got %d, baseline %d", event, got, want)
		}
	}
	routes := make(map[string]bool)
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"POST /auth/verify-code", "POST /api/auth/refresh", "POST /api/daemon-bootstrap-token",
		"GET /api/me/ducc", "POST /api/me/ducc", "POST /api/daemon/ducc/sync",
		"PUT /api/skills/{id}/scope", "POST /api/skills/{id}/promote",
		"POST /api/runtimes/{runtimeId}/delete-offline-machine",
	} {
		if !routes[route] {
			t.Errorf("standby router lost %s", route)
		}
	}

	// A production router still registers outbound channel delivery. WeCom
	// construction performs no database or network calls, unlike Lark backfill.
	for _, key := range keys {
		t.Setenv(key, "")
	}
	t.Setenv("MULTICA_WECOM_SECRET_KEY", fakeKey)
	t.Setenv("MULTICA_STANDBY", "false")
	activeBus := events.New()
	NewRouter(nil, realtime.NewHub(), activeBus, analytics.NoopClient{}, nil)
	if activeBus.SubscriberCount(protocol.EventChatDone) <= standbyBus.SubscriberCount(protocol.EventChatDone) {
		t.Fatal("production mode did not restore channel delivery subscriptions")
	}
}
