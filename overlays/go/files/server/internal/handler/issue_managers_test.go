package handler

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"sort"
	"testing"
)

func managerTestUser(t *testing.T, label string) string {
	t.Helper()
	if testHandler == nil {
		t.Fatal("isolated database required")
	}
	user := dbfx.User(t, label, "manager-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, user, "member")
	return user
}
func initializeManagers(t *testing.T, issue string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `SELECT team_mesh_ensure_issue_managers($1)`, issue); err != nil {
		t.Fatal(err)
	}
}
func managerIDs(t *testing.T, issue string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT user_id::text FROM issue_manager WHERE issue_id=$1 ORDER BY user_id`, issue)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return out
}
func requireManagerIDs(t *testing.T, issue string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := managerIDs(t, issue)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("administrator identities got %v want %v", got, want)
	}
}
