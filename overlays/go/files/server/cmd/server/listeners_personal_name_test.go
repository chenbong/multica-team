package main

import (
	"github.com/multica-ai/multica/server/internal/events"
	"testing"
)

func TestPersonalMachineNameOnlyNotifiesActor(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)
	bus.Publish(events.Event{Type: "daemon:personal-name", ActorID: "viewer", ActorType: "member", Payload: map[string]any{"action": "update"}})
	if len(fb.userCalls) != 1 || fb.userCalls[0].userID != "viewer" || len(fb.workspaceCalls) != 0 || len(fb.scopeCalls) != 0 || fb.broadcastCalled != 0 {
		t.Fatalf("personal event escaped user scope: %+v", fb)
	}
}
