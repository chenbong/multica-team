package handler

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func infoFlowDirectoryFixture(t *testing.T) (infoFlowTestFixture, infoFlowRobotObservation) {
	t.Helper()
	f := infoFlowConversationFixture(t)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM infoflow_robot_directory WHERE agent_id=$1`, f.req.AgentID) })
	enabled, available := true, true
	return f, infoFlowRobotObservation{infoFlowScope: f.req.infoFlowScope, RobotName: "Fixture private robot", RobotChatID: "9007199254740993", PrivateEnabled: &enabled, Available: &available}
}

func infoFlowDirectoryCall(t *testing.T, f infoFlowTestFixture, body any, token string, status int) {
	t.Helper()
	r := testutil.WithHeaders(testutil.JSONRequest("POST", "/robot-directory", body), "Authorization", "Bearer "+token)
	testutil.Call(t, f.h.InfoFlowRobotDirectory, r).Want(status)
}

func TestInfoFlowRobotDirectoryPreview(t *testing.T) {
	f, metadata := infoFlowDirectoryFixture(t)
	view := func() infoFlowPrivateRobotPreview {
		t.Helper()
		out := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
		if out.Private.Robot == nil {
			t.Fatal("missing private robot preview")
		}
		return *out.Private.Robot
	}
	if got := view(); got.State != "missing" || got.RobotID != "" {
		t.Fatalf("unexpected initial preview: %+v", got)
	}
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	if got := view(); got.State != "ready" || got.Name != metadata.RobotName || got.RobotID != metadata.RobotID || got.RobotChatID != metadata.RobotChatID {
		t.Fatalf("unexpected preview: %+v", got)
	}
	metadata.RobotName = "Renamed robot"
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_robot_directory WHERE agent_id=$1`, f.req.AgentID) != 1 || view().Name != metadata.RobotName {
		t.Fatal("heartbeat duplicated identity or failed to refresh name")
	}
	*metadata.PrivateEnabled = false
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	if view().State != "unavailable" {
		t.Fatal("disabled private chat advertised as ready")
	}
	*metadata.PrivateEnabled, *metadata.Available = true, false
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	if view().State != "unavailable" {
		t.Fatal("disconnected robot advertised as ready")
	}
	*metadata.Available = true
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	second := metadata
	second.RobotID, second.RobotName = uuid.NewString(), "Second robot"
	infoFlowDirectoryCall(t, f, second, infoFlowTestToken, 200)
	if got := view(); got.State != "ambiguous" || got.Name != "" || got.RobotID != "" {
		t.Fatalf("silently selected ambiguous robot: %+v", got)
	}
	dbfx.Exec(t, `UPDATE infoflow_robot_directory SET observed_at=clock_timestamp()-interval '6 minutes' WHERE robot_id=$1`, second.RobotID)
	if view().State != "ready" {
		t.Fatal("expired duplicate blocked unique live robot")
	}
	dbfx.Exec(t, `UPDATE infoflow_robot_directory SET observed_at=clock_timestamp()-interval '6 minutes' WHERE robot_id=$1`, metadata.RobotID)
	if got := view(); got.State != "unavailable" || got.Name != "" || got.RobotChatID != "" {
		t.Fatalf("expired robot exposed as current: %+v", got)
	}
}

func TestInfoFlowRobotDirectoryDoesNotLeakRoster(t *testing.T) {
	f, metadata := infoFlowDirectoryFixture(t)
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 200)
	other := dbfx.Agent(t, "Other agent", f.runtime, testutil.Cols{"owner_id": f.owner})
	foreign := metadata
	foreign.RobotID, foreign.AgentID, foreign.RobotName = uuid.NewString(), other, "Other assigned robot"
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM infoflow_robot_directory WHERE robot_id=$1`, foreign.RobotID) })
	infoFlowDirectoryCall(t, f, foreign, infoFlowTestToken, 200)
	out := infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if out.Private.Robot == nil || out.Private.Robot.RobotID != metadata.RobotID {
		t.Fatal("matched a workspace-wide robot instead of the assigned agent")
	}
	dbfx.Exec(t, `UPDATE agent SET visibility='private' WHERE id=$1`, f.req.AgentID)
	dbfx.Exec(t, `UPDATE member SET role='member' WHERE id=$1`, f.member)
	out = infoFlowConversationCall(t, f, "GET", f.user, "", nil, 200)
	if out.Private.CanManage || out.Private.Robot != nil {
		t.Fatal("robot metadata leaked without agent management authority")
	}
	dbfx.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, f.issue, f.owner)
	out = infoFlowConversationCall(t, f, "GET", f.owner, "", nil, 200)
	if out.Private.Eligible || out.Private.Robot != nil {
		t.Fatal("ineligible assignment exposed robot preview")
	}
}

func TestInfoFlowRobotDirectoryAdmission(t *testing.T) {
	f, metadata := infoFlowDirectoryFixture(t)
	infoFlowDirectoryCall(t, f, metadata, "invalid", 401)
	for _, mutation := range []func(*infoFlowRobotObservation){
		func(v *infoFlowRobotObservation) { v.AgentID = uuid.NewString() },
		func(v *infoFlowRobotObservation) { v.WorkspaceID = uuid.NewString() },
		func(v *infoFlowRobotObservation) { v.RobotName = "Bad\nname" },
		func(v *infoFlowRobotObservation) { v.RobotName = strings.Repeat("a", 257) },
		func(v *infoFlowRobotObservation) { v.RobotChatID = "9223372036854775808" },
		func(v *infoFlowRobotObservation) { v.RobotID = " " },
		func(v *infoFlowRobotObservation) { v.PrivateEnabled = nil },
		func(v *infoFlowRobotObservation) { v.Available = nil },
	} {
		bad := metadata
		mutation(&bad)
		infoFlowDirectoryCall(t, f, bad, infoFlowTestToken, 400)
	}
	// Unknown credential fields are refused rather than silently retained.
	r := testutil.WithHeaders(httptest.NewRequest("POST", "/robot-directory", strings.NewReader(`{"app_secret":"fixture-only"}`)), "Authorization", "Bearer "+infoFlowTestToken)
	testutil.Call(t, f.h.InfoFlowRobotDirectory, r).Want(400)
	dbfx.Exec(t, `UPDATE agent SET archived_at=clock_timestamp() WHERE id=$1`, f.req.AgentID)
	infoFlowDirectoryCall(t, f, metadata, infoFlowTestToken, 400)
	if infoFlowCount(t, `SELECT count(*) FROM infoflow_robot_directory WHERE agent_id=$1`, f.req.AgentID) != 0 {
		t.Fatal("invalid observations wrote metadata")
	}
}

func TestInfoFlowRobotDirectoryMigrations(t *testing.T) {
	f, _ := infoFlowDirectoryFixture(t)
	_ = f // The fixture rejects any non-isolated database before schema writes.
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	schema := "robot_directory_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "SET search_path TO public")
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+quoted)
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	run := func(name, direction string) {
		t.Helper()
		raw, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", name+"."+direction+".sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = conn.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	for range 2 {
		run("513_infoflow_robot_directory", "up")
		run("514_infoflow_robot_directory_identity", "up")
	}
	run("514_infoflow_robot_directory_identity", "down")
	run("513_infoflow_robot_directory", "down")
	for range 2 {
		run("514_infoflow_robot_directory_identity", "down")
		run("513_infoflow_robot_directory", "down")
	}
}
