package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type panelWorkspaceDeleteFixture struct {
	workspace, issue, workspacePanel, issuePanel string
}

func newPanelWorkspaceDeleteFixture(t *testing.T, role string) panelWorkspaceDeleteFixture {
	t.Helper()
	webPanelDB(t)
	ws := dbfx.Workspace(t, "Panel workspace delete", "panel-delete-"+uuid.NewString())
	dbfx.Member(t, ws, testUserID, role)
	issue := dbfx.Issue(t, "Panel delete child", testutil.Cols{"workspace_id": ws})
	workspacePanel := dbfx.Insert(t, "resource_web_panel", testutil.Cols{
		"id": uuid.NewString(), "workspace_id": ws, "created_by": testUserID,
		"name": "Workspace page", "url": "https://example.test/workspace/" + uuid.NewString(),
	})
	issuePanel := dbfx.Insert(t, "resource_web_panel", testutil.Cols{
		"id": uuid.NewString(), "issue_id": issue, "created_by": testUserID,
		"name": "Issue page", "url": "https://example.test/issue/" + uuid.NewString(),
	})
	return panelWorkspaceDeleteFixture{ws, issue, workspacePanel, issuePanel}
}

func callPanelWorkspaceDelete(t *testing.T, h *Handler, ws string, status int) {
	t.Helper()
	r := newRequest(http.MethodDelete, "/api/workspaces/"+ws, nil)
	r.Header.Set("X-Workspace-ID", ws)
	r = testutil.WithURLParams(r, "id", ws)
	// Exercise the complete HTTP handler and its teardown transaction, rather
	// than calling only Queries.DeleteWorkspace after bypassing earlier steps.
	testutil.Call(t, h.DeleteWorkspace, r).Want(status)
}

func TestResourceWebPanelWorkspaceDeleteHTTPRemovesBothScopes(t *testing.T) {
	f := newPanelWorkspaceDeleteFixture(t, "owner")
	other := newPanelWorkspaceDeleteFixture(t, "owner")
	moved := dbfx.Issue(t, "Transferred panel child", testutil.Cols{"workspace_id": f.workspace})
	movedPanel := dbfx.Insert(t, "resource_web_panel", testutil.Cols{
		"id": uuid.NewString(), "issue_id": moved, "created_by": testUserID,
		"name": "Transferred page", "url": "https://example.test/transferred/" + uuid.NewString(),
	})
	dbfx.Exec(t, `UPDATE issue SET workspace_id=$2 WHERE id=$1`, moved, other.workspace)

	callPanelWorkspaceDelete(t, testHandler, f.workspace, http.StatusNoContent)
	if n := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE id=$1`, f.workspace); n != 0 {
		t.Fatal("workspace survived the successful HTTP delete")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1`, f.issue); n != 0 {
		t.Fatal("issue survived the successful HTTP delete")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM resource_web_panel WHERE id IN ($1,$2)`, f.workspacePanel, f.issuePanel); n != 0 {
		t.Fatalf("HTTP workspace delete left %d orphaned panels", n)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM resource_web_panel WHERE id IN ($1,$2,$3)`, other.workspacePanel, other.issuePanel, movedPanel); n != 3 {
		t.Fatal("workspace deletion removed foreign or transferred panels")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workspace_id=$2`, moved, other.workspace); n != 1 {
		t.Fatal("workspace deletion removed a transferred issue")
	}
}

func TestResourceWebPanelWorkspaceDeleteHTTPRequiresOwner(t *testing.T) {
	for _, role := range []string{"admin", "member"} {
		t.Run(role, func(t *testing.T) {
			f := newPanelWorkspaceDeleteFixture(t, role)
			callPanelWorkspaceDelete(t, testHandler, f.workspace, http.StatusForbidden)
			if n := dbfx.Count(t, `SELECT count(*) FROM resource_web_panel WHERE id IN ($1,$2)`, f.workspacePanel, f.issuePanel); n != 2 {
				t.Fatal("unauthorized workspace delete changed panels")
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1`, f.issue); n != 1 {
				t.Fatal("unauthorized workspace delete changed the issue")
			}
		})
	}
}

type panelWorkspaceDeleteFailStarter struct {
	delegate                                 txStarter
	workspace, issue                         string
	observed                                 bool
	panelsBeforeFailure, issuesBeforeFailure int
}

type panelWorkspaceDeleteFailTx struct {
	pgx.Tx
	starter *panelWorkspaceDeleteFailStarter
}

func (s *panelWorkspaceDeleteFailStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.delegate.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &panelWorkspaceDeleteFailTx{Tx: tx, starter: s}, nil
}

func (tx *panelWorkspaceDeleteFailTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(query, "-- name: DeleteIssueStatusEntriesForWorkspace") {
		s := tx.starter
		s.observed = true
		if err := tx.Tx.QueryRow(ctx, `SELECT count(*) FROM resource_web_panel WHERE workspace_id=$1 OR issue_id=$2`, s.workspace, s.issue).Scan(&s.panelsBeforeFailure); err != nil {
			return pgconn.CommandTag{}, err
		}
		if err := tx.Tx.QueryRow(ctx, `SELECT count(*) FROM issue WHERE id=$1`, s.issue).Scan(&s.issuesBeforeFailure); err != nil {
			return pgconn.CommandTag{}, err
		}
		return pgconn.CommandTag{}, errors.New("injected failure after issue root deletion")
	}
	return tx.Tx.Exec(ctx, query, args...)
}

func TestResourceWebPanelWorkspaceDeleteHTTPRollsBackPanelCleanup(t *testing.T) {
	f := newPanelWorkspaceDeleteFixture(t, "owner")
	starter := &panelWorkspaceDeleteFailStarter{delegate: testPool, workspace: f.workspace, issue: f.issue}
	h := *testHandler
	h.TxStarter = starter
	callPanelWorkspaceDelete(t, &h, f.workspace, http.StatusInternalServerError)
	if !starter.observed || starter.panelsBeforeFailure != 0 || starter.issuesBeforeFailure != 0 {
		t.Fatalf("cleanup must share the issue-deletion transaction before the injected failure: observed=%v panels=%d issues=%d", starter.observed, starter.panelsBeforeFailure, starter.issuesBeforeFailure)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM resource_web_panel WHERE id IN ($1,$2)`, f.workspacePanel, f.issuePanel); n != 2 {
		t.Fatal("failed workspace deletion did not restore both panels")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1`, f.issue); n != 1 {
		t.Fatal("failed workspace deletion did not restore the issue")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE id=$1`, f.workspace); n != 1 {
		t.Fatal("failed workspace deletion did not restore the workspace")
	}
}
