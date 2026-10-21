package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type infoFlowConversation struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type infoFlowScope struct {
	RobotID     string `json:"robot_id"`
	AgentID     string `json:"agent_id"`
	WorkspaceID string `json:"workspace_id"`
}

type infoFlowRelayRequest struct {
	infoFlowScope
	Conversation       infoFlowConversation `json:"conversation"`
	Sender             string               `json:"sender"`
	MessageID          string               `json:"message_id"`
	Operation          string               `json:"operation"`
	Command            string               `json:"command,omitempty"`
	StatusPage         int                  `json:"status_page,omitempty"`
	IssueRef           string               `json:"issue_ref,omitempty"`
	Content            string               `json:"content,omitempty"`
	ReplyToMessageID   string               `json:"reply_to_message_id,omitempty"`
	RequireRobotQuote  bool                 `json:"require_robot_quote,omitempty"`
	HasAttachments     bool                 `json:"has_attachments,omitempty"`
	Attachments        []infoFlowAttachment `json:"attachments,omitempty"`
	ExpectedBindingID  string               `json:"expected_binding_id,omitempty"`
	ExpectedGeneration string               `json:"expected_generation,omitempty"`
	RobotChatID        string               `json:"robot_chat_id,omitempty"`
	RobotName          string               `json:"robot_name,omitempty"`
	NotifyUsers        []string             `json:"notify_users,omitempty"`
}

type infoFlowBindingView struct {
	BindingID     string               `json:"binding_id"`
	Group         *infoFlowGroupView   `json:"group,omitempty"`
	Private       *infoFlowPrivateView `json:"private,omitempty"`
	IssueID       string               `json:"issue_id"`
	Identifier    string               `json:"identifier"`
	WorkspaceSlug string               `json:"workspace_slug"`
	Title         string               `json:"title,omitempty"`
	Generation    string               `json:"generation"`
}

type infoFlowRelayResponse struct {
	InputReceived      *bool                `json:"input_received,omitempty"`
	CanReconcileGroup  bool                 `json:"can_reconcile_group,omitempty"`
	BindingRevalidated bool                 `json:"binding_revalidated,omitempty"`
	ConversationMode   string               `json:"conversation_mode,omitempty"`
	Handled            bool                 `json:"handled"`
	Silent             bool                 `json:"silent"`
	CanComment         bool                 `json:"can_comment"`
	Notice             string               `json:"notice,omitempty"`
	Binding            *infoFlowBindingView `json:"binding,omitempty"`
	CommentID          string               `json:"comment_id,omitempty"`
	SuppressIMNotice   bool                 `json:"suppress_im_notice,omitempty"`
	NoticeKind         string               `json:"notice_kind,omitempty"`
	TaskChoices        []infoFlowTaskChoice `json:"task_choices,omitempty"`
	StatusPage         int                  `json:"status_page,omitempty"`
}

// Routing metadata is separate from the retained issue link on unbind notices.
// Only the IM bridge renders a mode label; issue/comment content stays untouched.
func infoFlowConversationMode(b infoFlowBinding) string {
	if b.IssueID.Valid {
		return "issue"
	}
	return "chat"
}

type infoFlowBinding struct {
	ID                pgtype.UUID
	IssueID           pgtype.UUID
	BinderID          pgtype.UUID
	Generation        pgtype.UUID
	BoundAt           time.Time
	Kind              string
	GroupTopology     []byte
	GroupState        string
	LeaderRobotID     string
	LeaderVerifiedAt  pgtype.Timestamptz
	AssignmentVersion int64
	RobotChatID       string
	RobotName         string
	SyncEnabled       bool
}

type infoFlowPrivateView struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// The caller's transaction holds the issue fence through admission/delivery.
// Only an explicit bind may update the saved version, including after A->B->A.
func (h *Handler) infoFlowPrivateView(ctx context.Context, b infoFlowBinding, wsID, agentID string) *infoFlowPrivateView {
	v := &infoFlowPrivateView{State: "needs_rebind"}
	var kind, assignee string
	var version int64
	err := h.DB.QueryRow(ctx, `SELECT COALESCE(assignee_type,''),COALESCE(assignee_id::text,''),infoflow_assignment_version FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, b.IssueID, wsID).Scan(&kind, &assignee, &version)
	if err != nil {
		v.Reason = "任务已失效或暂时无法确认负责人，请重新绑定"
		return v
	}
	if kind != "agent" {
		v.Reason = "私聊绑定仅支持由单独一个智能体负责的任务，请改用群聊或重新分配任务"
		return v
	}
	if !strings.EqualFold(assignee, agentID) || version != b.AssignmentVersion {
		v.Reason = "任务负责人已变化，私聊绑定需要重新绑定"
		return v
	}
	var live pgtype.UUID
	if err = h.DB.QueryRow(ctx, `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL FOR SHARE`, assignee, wsID).Scan(&live); err != nil {
		v.Reason = "任务负责智能体不可用，请重新绑定"
		return v
	}
	v.State = "active"
	return v
}

var infoFlowUsername = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var infoFlowAccountLetter = regexp.MustCompile(`[a-z]`)
var infoFlowDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)

// Preserve the complete intended audience; the Bridge limits a single AT
// block to 50 and reports omitted recipients without dropping the task.
const infoFlowMaxNotifyUsers = 5000

// The authenticated bridge supplies human UUAP accounts, never display names,
// numeric robot IDs, UUIDs, or broadcast tokens. This is not an identity lookup.
func infoFlowHumanAccount(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !infoFlowUsername.MatchString(value) || !infoFlowAccountLetter.MatchString(value) {
		return ""
	}
	switch value {
	case "atall", "all", "robot", "bot":
		return ""
	}
	if _, err := util.ParseUUID(value); err == nil {
		return ""
	}
	return value
}

func infoFlowNormalizeNotifyUsers(sender string, users []string, robotID string) []string {
	out := make([]string, 0, len(users)+1)
	seen := make(map[string]bool, len(users)+1)
	for _, value := range append([]string{sender}, users...) {
		account := infoFlowHumanAccount(value)
		if account == "" || strings.EqualFold(account, robotID) || seen[account] {
			continue
		}
		seen[account] = true
		out = append(out, account)
	}
	return out
}

func infoFlowValidPrivateRobotMetadata(chatID, name string) bool {
	if !infoFlowValidGroupID(chatID) || strings.TrimSpace(name) == "" || len(name) > 256 {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// This is deliberately independent of ordinary Auth and its identity headers.
func infoFlowAuthenticate(w http.ResponseWriter, r *http.Request) bool {
	for _, key := range []string{"X-User-ID", "X-User-Email", "X-Agent-ID", "X-Task-ID", "X-Actor-Source", "X-Workspace-ID"} {
		r.Header.Del(key)
	}
	token := os.Getenv("INFOFLOW_ISSUE_RELAY_TOKEN")
	domain := os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")
	if len(token) < 32 || len(domain) > 253 || !infoFlowDomain.MatchString(domain) {
		infoFlowError(w, http.StatusServiceUnavailable, "issue relay unavailable")
		return false
	}
	value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	expected, actual := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(value))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		infoFlowError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func infoFlowError(w http.ResponseWriter, status int, _ string) {
	// Credentials, invalid input and storage errors never become Chat input or
	// expose an upstream handler body, SQL error, or partially created object.
	writeJSON(w, status, infoFlowRelayResponse{Handled: true, Silent: true})
}

func infoFlowDecode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 29<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		infoFlowError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		infoFlowError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func (s infoFlowScope) valid() bool {
	_, ae := util.ParseUUID(s.AgentID)
	_, we := util.ParseUUID(s.WorkspaceID)
	return ae == nil && we == nil && s.RobotID != "" && len(s.RobotID) <= 256 && !strings.ContainsRune(s.RobotID, 0)
}

func (s infoFlowScope) lockKey(c infoFlowConversation) string {
	if c.Kind == "group" {
		b, _ := json.Marshal([]string{c.Kind, c.ID})
		return "infoflow-issue-group:" + string(b)
	}
	b, _ := json.Marshal([]string{s.RobotID, strings.ToLower(s.WorkspaceID), strings.ToLower(s.AgentID), c.Kind, c.ID})
	return "infoflow-issue:" + string(b)
}

func infoFlowLock(ctx context.Context, tx pgx.Tx, s infoFlowScope, c infoFlowConversation) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, s.lockKey(c))
	return err
}

// Use a new request, not a cloned header map. No externally supplied actor,
// originator, task, workspace, capability or authentication header can survive.
func infoFlowMemberRequest(ctx context.Context, userID, workspaceID, issueID string, body []byte) *http.Request {
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/issues", bytes.NewReader(body))
	r.Header.Set("X-User-ID", userID)
	r.Header.Set("X-Workspace-ID", workspaceID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Client-Capabilities", ClientCapabilityStableAttachmentURLs)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", issueID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	ctx = context.WithValue(ctx, wakeupActorKey{}, wakeupActor{kind: "member", id: userID})
	return r.WithContext(ctx)
}

type infoFlowCapture struct {
	header http.Header
	bytes.Buffer
	status int
}

func (c *infoFlowCapture) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}
func (c *infoFlowCapture) WriteHeader(s int) {
	if c.status == 0 {
		c.status = s
	}
}
func (c *infoFlowCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	if c.Len()+len(p) > 1<<20 {
		return 0, errors.New("relay handler response too large")
	}
	return c.Buffer.Write(p)
}

// Nested Begin calls create real savepoints. The outer transaction owns the
// comment/issue, tasks, receipts and source mapping. No event listener or daemon
// can see a write until that outer transaction commits.
func (h *Handler) infoFlowScoped(tx pgx.Tx) (*Handler, func()) {
	tx = &infoFlowSavepointTx{Tx: tx}
	clone := *h
	clone.Queries, clone.DB, clone.TxStarter = h.Queries.WithTx(tx), tx, tx
	clone.ReadSelector, clone.MembershipCache = nil, nil
	clone.IssueStatusCatalog = clone.Queries
	bus := events.New()
	var pending []events.Event
	bus.SubscribeAll(func(e events.Event) { pending = append(pending, e) })
	clone.Bus = bus
	wakeup := &infoFlowWakeups{}
	ts := service.NewTaskService(clone.Queries, tx, h.Hub, bus, wakeup)
	if h.TaskService != nil {
		ts.Entitlements = h.TaskService.Entitlements
		ts.FeatureFlags = h.TaskService.FeatureFlags
		ts.Composio = h.TaskService.Composio
	}
	clone.TaskService = ts
	clone.IssueService = service.NewIssueService(clone.Queries, tx, bus, nil, ts)
	clone.IssueService.Entitlements = h.Entitlements
	return &clone, func() {
		if h.Bus != nil {
			for _, e := range pending {
				h.Bus.Publish(e)
			}
		}
		if h.TaskService != nil {
			for _, v := range wakeup.pending {
				h.TaskService.EmptyClaim.Bump(context.Background(), v[0])
				if h.TaskService.Wakeup != nil {
					h.TaskService.Wakeup.NotifyTaskAvailable(v[0], v[1])
				}
			}
		}
	}
}

type infoFlowWakeups struct{ pending [][2]string }

func (w *infoFlowWakeups) NotifyTaskAvailable(runtimeID, taskID string) {
	w.pending = append(w.pending, [2]string{runtimeID, taskID})
}

func (h *Handler) infoFlowIdentity(ctx context.Context, sender, workspaceID string) (db.User, string, error) {
	// Platform users are established by the platform's verified-email login.
	// The relay never provisions accounts or accepts a caller-provided user id.
	user, err := h.Queries.GetUserByEmail(ctx, strings.ToLower(sender+"@"+os.Getenv("INFOFLOW_ISSUE_IDENTITY_DOMAIN")))
	if err != nil {
		return db.User{}, "", err
	}
	if auth.IsTemporarilyDisabledUser(uuidToString(user.ID), user.Email) {
		return db.User{}, "", pgx.ErrNoRows
	}
	var role string
	err = h.DB.QueryRow(ctx, `SELECT role FROM member WHERE user_id=$1 AND workspace_id=$2 FOR SHARE`, user.ID, workspaceID).Scan(&role)
	return user, role, err
}

func (h *Handler) infoFlowBindingView(ctx context.Context, b infoFlowBinding, wsID string, full bool) *infoFlowBindingView {
	if !b.IssueID.Valid {
		return nil
	}
	var out infoFlowBindingView
	var title string
	err := h.DB.QueryRow(ctx, `SELECT i.id::text,w.issue_prefix || '-' || i.number::text,w.slug,i.title
		FROM issue i JOIN workspace w ON w.id=i.workspace_id WHERE i.id=$1 AND i.workspace_id=$2`, b.IssueID, wsID).Scan(&out.IssueID, &out.Identifier, &out.WorkspaceSlug, &title)
	if err != nil {
		return nil
	}
	out.Generation = uuidToString(b.Generation)
	out.BindingID = uuidToString(b.ID)
	if b.Kind == "group" {
		out.Group = h.infoFlowGroupView(ctx, b, wsID)
	} else if b.Kind == "private" {
		var agentID string
		if err := h.DB.QueryRow(ctx, `SELECT agent_id::text FROM infoflow_issue_binding WHERE id=$1 AND workspace_id=$2`, b.ID, wsID).Scan(&agentID); err != nil {
			out.Private = &infoFlowPrivateView{State: "needs_rebind", Reason: "私聊绑定已失效，请重新绑定"}
		} else {
			out.Private = h.infoFlowPrivateView(ctx, b, wsID, agentID)
		}
	}
	if full {
		out.Title = title
	}
	return &out
}

func (h *Handler) infoFlowOwner(ctx context.Context, wsID string, agent *db.Agent) string {
	var name string
	var err error
	if agent != nil {
		err = h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(name,''),email) FROM "user" WHERE id=$1`, agent.OwnerID).Scan(&name)
	} else {
		err = h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(u.name,''),u.email) FROM member m JOIN "user" u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.role='owner' ORDER BY m.id LIMIT 1`, wsID).Scan(&name)
	}
	if err != nil || name == "" {
		if agent != nil {
			return "智能体 owner"
		}
		return "工作区 owner"
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '[' || r == ']' {
			return ' '
		}
		return r
	}, name)
}

// InfoFlowIssueRelay is the server-authoritative bridge admission boundary.
func (h *Handler) InfoFlowIssueRelay(w http.ResponseWriter, r *http.Request) {
	if !infoFlowAuthenticate(w, r) {
		return
	}
	var req infoFlowRelayRequest
	if !infoFlowDecode(w, r, &req) {
		return
	}
	if !req.infoFlowScope.valid() || !infoFlowUsername.MatchString(req.Sender) || req.Conversation.ID == "" || len(req.Conversation.ID) > 256 || strings.ContainsRune(req.Conversation.ID, 0) ||
		(req.Conversation.Kind != "private" && req.Conversation.Kind != "group") || (req.Conversation.Kind == "private" && req.Conversation.ID != req.Sender) || len(req.MessageID) > 256 || strings.ContainsRune(req.MessageID, 0) || len(req.Content) > 65536 || len(req.ReplyToMessageID) > 256 {
		infoFlowError(w, 400, "invalid request")
		return
	}
	if req.Conversation.Kind == "group" && !infoFlowValidGroupID(req.Conversation.ID) {
		infoFlowError(w, 400, "group id")
		return
	}
	var notifyUsers = []string{}
	if req.Conversation.Kind == "group" {
		if len(req.NotifyUsers) > infoFlowMaxNotifyUsers || infoFlowHumanAccount(req.Sender) == "" || strings.EqualFold(req.Sender, req.RobotID) {
			infoFlowError(w, 400, "invalid notification accounts")
			return
		}
		notifyUsers = infoFlowNormalizeNotifyUsers(req.Sender, req.NotifyUsers, req.RobotID)
		if len(notifyUsers) > infoFlowMaxNotifyUsers {
			infoFlowError(w, 400, "too many notification accounts")
			return
		}
		// Omitted/empty fields must remain omitted in the historical request
		// hash. Explicit lists are canonicalized before hashing and storage.
		if len(req.NotifyUsers) > 0 {
			// Retain the historical request fingerprint for retries while
			// storing sender-first instruction order separately below.
			req.NotifyUsers = slices.Clone(notifyUsers)
			slices.Sort(req.NotifyUsers)
		}
	} else {
		req.NotifyUsers = nil
	}
	switch req.Operation {
	case "message", "bind", "unbind", "create":
		if req.MessageID == "" {
			infoFlowError(w, 400, "missing message id")
			return
		}
	case "binding":
	default:
		infoFlowError(w, 400, "invalid operation")
		return
	}
	if req.RobotChatID != "" || req.RobotName != "" {
		// Only the trusted Bridge may attach SDK-confirmed public metadata to
		// an explicit private bind/create. It never changes sender or routing.
		if req.Conversation.Kind != "private" || (req.Operation != "bind" && req.Operation != "create") || !infoFlowValidPrivateRobotMetadata(req.RobotChatID, req.RobotName) {
			infoFlowError(w, 400, "private robot metadata")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		infoFlowError(w, 500, "begin")
		return
	}
	defer tx.Rollback(r.Context())
	if err = infoFlowLock(r.Context(), tx, req.infoFlowScope, req.Conversation); err != nil {
		infoFlowError(w, 500, "lock")
		return
	}
	sh, flush := h.infoFlowScoped(tx)
	if req.RequireRobotQuote {
		if req.Conversation.Kind != "group" || req.ReplyToMessageID == "" || req.Command != "" || (req.Operation != "binding" && req.Operation != "message") {
			infoFlowError(w, 400, "invalid robot quote")
			return
		}
		var known bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(
 SELECT 1 FROM infoflow_issue_delivery d JOIN infoflow_issue_binding b ON b.id=d.binding_id
 WHERE b.workspace_id=$1 AND b.conversation_kind='group' AND b.conversation_id=$2
 AND b.issue_id IS NOT NULL AND b.generation=d.generation
 AND (d.external_message_id=$3 OR $3=ANY(d.external_message_ids))
 UNION ALL
 SELECT 1 FROM infoflow_conversation_notice n JOIN infoflow_issue_binding b ON b.id=n.binding_id
 WHERE n.workspace_id=$1 AND n.conversation_kind='group' AND n.conversation_id=$2
 AND n.kind='bound' AND b.issue_id IS NOT NULL AND b.generation=n.generation
 AND $3=ANY(n.external_message_ids))`, req.WorkspaceID, req.Conversation.ID, req.ReplyToMessageID).Scan(&known)
		if err != nil {
			infoFlowError(w, 500, "robot quote lookup")
			return
		}
		if !known {
			writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true})
			return
		}
	}
	if req.Operation == "binding" && (req.Command == "help" || req.Command == "status") {
		sh.infoFlowConversationCommand(w, r, tx, req)
		return
	}
	var b infoFlowBinding
	{
		var notice string
		b, notice, err = sh.infoFlowRouteGroup(r.Context(), tx, req)
		var choiceNotice *infoFlowTaskChoiceNotice
		if errors.As(err, &choiceNotice) {
			notice, err = choiceNotice.Error(), nil
		}
		if errors.Is(err, errInfoFlowGroupEcho) {
			writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true, ConversationMode: "issue"})
			return
		}
		if errors.Is(err, errInfoFlowRouteChanged) {
			status := 409
			if _, e := util.ParseUUID(req.ExpectedBindingID); e != nil {
				status = 400
			}
			infoFlowError(w, status, "route changed")
			return
		}
		if err == nil && notice != "" {
			if req.ExpectedBindingID != "" || req.ExpectedGeneration != "" {
				infoFlowError(w, 409, "pinned route unavailable")
				return
			}
			out := infoFlowRelayResponse{Handled: true, ConversationMode: "issue", Notice: notice}
			if _, _, e := sh.infoFlowIdentity(r.Context(), req.Sender, req.WorkspaceID); e != nil {
				if !errors.Is(e, pgx.ErrNoRows) {
					infoFlowError(w, 500, "identity")
					return
				}
				out.Notice = "没有工作区权限，无法评论，请向[" + sh.infoFlowOwner(r.Context(), req.WorkspaceID, nil) + "]申请工作区权限"
			} else if choiceNotice != nil {
				out.NoticeKind = "task_choices"
				out.TaskChoices, err = sh.infoFlowTaskChoices(r.Context(), tx, req.WorkspaceID, choiceNotice.Bindings)
				if err != nil {
					infoFlowError(w, 500, "task choices")
					return
				}
			}
			writeJSON(w, 200, out)
			return
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		infoFlowError(w, 500, "binding")
		return
	}
	// Lock the issue in its eventual write mode before topology/private views
	// take SHARE locks. Concurrent conversations cannot deadlock upgrading them.
	if b.IssueID.Valid {
		lock := "FOR SHARE"
		if req.Operation == "message" {
			lock = "FOR NO KEY UPDATE"
		}
		var live pgtype.UUID
		if e := tx.QueryRow(r.Context(), `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 `+lock, b.IssueID, req.WorkspaceID).Scan(&live); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			infoFlowError(w, 500, "issue fence")
			return
		}
	}
	// Lock the agent before its grants, matching owner updates. Revocation of
	// either permission_mode or an existing target waits for the accepted write.
	var lockedAgent pgtype.UUID
	err = tx.QueryRow(r.Context(), `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 FOR SHARE`, req.AgentID, req.WorkspaceID).Scan(&lockedAgent)
	if err != nil {
		infoFlowError(w, 409, "agent unavailable")
		return
	}
	grantRows, err := tx.Query(r.Context(), `SELECT target_type,target_id FROM agent_invocation_target WHERE agent_id=$1 ORDER BY target_type,target_id FOR SHARE`, req.AgentID)
	if err != nil {
		infoFlowError(w, 500, "agent permission")
		return
	}
	for grantRows.Next() {
	}
	err = grantRows.Err()
	grantRows.Close()
	if err != nil {
		infoFlowError(w, 500, "agent permission")
		return
	}
	agent, err := sh.Queries.GetAgent(r.Context(), parseUUID(req.AgentID))
	if err != nil || uuidToString(agent.WorkspaceID) != strings.ToLower(req.WorkspaceID) || agent.ArchivedAt.Valid {
		infoFlowError(w, 409, "agent unavailable")
		return
	}
	// Workspace permissions govern bound issue traffic, not the legacy Chat
	// path. Inspect receipt existence before this bypass: an accepted message
	// retried after unbind must remain relay-owned even after membership loss.
	createPreflight := req.Operation == "binding" && req.Command == "create"
	var acceptedBeforeUnbind bool
	if !b.IssueID.Valid && !createPreflight && (req.Operation == "message" || req.Operation == "binding") {
		if b.ID.Valid && req.MessageID != "" {
			err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_source WHERE binding_id=$1 AND message_id=$2)`, b.ID, req.MessageID).Scan(&acceptedBeforeUnbind)
			if err != nil {
				infoFlowError(w, 500, "receipt")
				return
			}
			var echo bool
			err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_delivery WHERE binding_id=$1 AND (external_message_id=$2 OR $2=ANY(external_message_ids)))`, b.ID, req.MessageID).Scan(&echo)
			if err != nil {
				infoFlowError(w, 500, "echo")
				return
			}
			if echo {
				writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true})
				return
			}
		}
		if !acceptedBeforeUnbind && req.ExpectedBindingID == "" && req.ExpectedGeneration == "" {
			out := infoFlowRelayResponse{ConversationMode: "chat"}
			if req.Operation == "binding" && req.Command != "" {
				out.Handled = true
				out.Notice = "当前会话没有绑定任务"
			}
			writeJSON(w, 200, out)
			return
		}
	}
	user, role, identityErr := sh.infoFlowIdentity(r.Context(), req.Sender, req.WorkspaceID)
	// Multiple bound conversations may submit to one issue concurrently. Acquire the
	// eventual comment-write lock before reading topology; two FOR SHARE locks
	// upgraded later by CreateComment would otherwise deadlock each other.
	if identityErr == nil && req.Operation == "message" && b.IssueID.Valid {
		var live pgtype.UUID
		e := tx.QueryRow(r.Context(), `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, b.IssueID, req.WorkspaceID).Scan(&live)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			infoFlowError(w, 500, "issue admission lock")
			return
		}
	}
	resp := infoFlowRelayResponse{Handled: true, CanComment: identityErr == nil, ConversationMode: infoFlowConversationMode(b)}
	if !b.IssueID.Valid {
		var remaining bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind=$2 AND conversation_id=$1 AND issue_id IS NOT NULL AND ($2='group' OR (robot_id=$3 AND workspace_id=$4 AND agent_id::text=lower($5))))`, req.Conversation.ID, req.Conversation.Kind, req.RobotID, req.WorkspaceID, req.AgentID).Scan(&remaining); err != nil {
			infoFlowError(w, 500, "remaining")
			return
		}
		if remaining {
			resp.ConversationMode = "issue"
		}
	}
	resp.Binding = sh.infoFlowBindingView(r.Context(), b, req.WorkspaceID, identityErr == nil)
	if identityErr != nil {
		if !errors.Is(identityErr, pgx.ErrNoRows) {
			infoFlowError(w, 500, "identity")
			return
		}
		resp.Notice = "没有工作区权限，无法评论，请向[" + sh.infoFlowOwner(r.Context(), req.WorkspaceID, nil) + "]申请工作区权限"
		writeJSON(w, 200, resp)
		return
	}
	r.Header.Set("X-User-ID", uuidToString(user.ID))
	r.Header.Set("X-User-Email", user.Email)
	canManage := uuidToString(agent.OwnerID) == uuidToString(user.ID) || role == "owner" || role == "admin" || (b.BinderID.Valid && b.BinderID == user.ID)
	if req.Conversation.Kind == "group" && b.IssueID.Valid {
		canManage = role == "owner" || role == "admin" || (b.BinderID.Valid && b.BinderID == user.ID)
		if resp.Binding != nil && resp.Binding.Group != nil && resp.Binding.Group.LeaderAgentID != "" {
			if leader, e := sh.Queries.GetAgent(r.Context(), parseUUID(resp.Binding.Group.LeaderAgentID)); e == nil && leader.OwnerID == user.ID {
				canManage = true
			}
		}
	}
	if createPreflight {
		// Read-only media admission for /create, including unbound sessions.
		// CanComment here means media may be downloaded for the create request;
		// the actual mutation repeats all gates under its own transaction.
		if !canManage {
			resp.CanComment = false
			resp.Notice = "只有智能体所有者、工作区管理员或当前绑定人可以修改任务绑定"
		} else if !sh.canInvokeAgent(r.Context(), agent, "member", uuidToString(user.ID), "", req.WorkspaceID) {
			resp.CanComment = false
			resp.Notice = "没有智能体权限，无法创建并调用任务，请向[" + sh.infoFlowOwner(r.Context(), req.WorkspaceID, &agent) + "]申请智能体权限"
		}
		writeJSON(w, 200, resp)
		return
	}
	if b.IssueID.Valid && resp.Binding == nil && req.Operation != "unbind" && req.Operation != "bind" && req.Operation != "create" {
		resp.Notice = "任务绑定已失效，请重新绑定任务"
		resp.CanComment = false
		writeJSON(w, 200, resp)
		return
	}
	if req.Operation == "binding" {
		if req.ExpectedBindingID != "" || req.ExpectedGeneration != "" {
			var accepted bool
			if b.ID.Valid && req.MessageID != "" {
				if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_source WHERE binding_id=$1 AND message_id=$2)`, b.ID, req.MessageID).Scan(&accepted); err != nil {
					infoFlowError(w, 500, "preflight receipt")
					return
				}
			}
			resp.InputReceived = &accepted
			if !accepted && (req.ExpectedBindingID != uuidToString(b.ID) || req.ExpectedGeneration != uuidToString(b.Generation) || !b.IssueID.Valid) {
				infoFlowError(w, 409, "preflight version")
				return
			}
		}
		resp.CanReconcileGroup = req.Conversation.Kind == "group" && b.IssueID.Valid && canManage
		if resp.Binding != nil && resp.Binding.Group != nil {
			g := resp.Binding.Group
			if g.State != "active" {
				resp.CanComment = false
				resp.Notice = g.Reason
			} else if !slices.Contains(g.AgentIDs, strings.ToLower(req.AgentID)) {
				resp.CanComment = false
				resp.Notice = "该机器人不属于任务负责人或当前小队，请联系队长机器人"
			}
		}
		if resp.Binding != nil && resp.Binding.Private != nil && resp.Binding.Private.State != "active" {
			resp.CanComment = false
			resp.Notice = resp.Binding.Private.Reason
		}
		if !b.IssueID.Valid {
			// This is a preflight for an already accepted input, not a new
			// unbound Chat message. Do not redownload media or fall through.
			resp.Handled, resp.Silent, resp.CanComment = true, true, false
		}
		if req.Command == "task_status" && b.IssueID.Valid {
			choices, e := sh.infoFlowTaskChoices(r.Context(), tx, req.WorkspaceID, []infoFlowBinding{b})
			if e != nil {
				infoFlowError(w, 500, "task status")
				return
			}
			if len(choices) == 1 {
				resp.Notice = choices[0].Identifier + "：" + choices[0].Status
			}
		}
		writeJSON(w, 200, resp)
		return
	}
	// Cached mutation responses are read only AFTER the current identity gate.
	raw, _ := json.Marshal(req)
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	if b.ID.Valid {
		var savedHash, savedSender string
		var savedGeneration pgtype.UUID
		var saved []byte
		err = tx.QueryRow(r.Context(), `SELECT request_hash,sender,response,generation FROM infoflow_issue_source WHERE binding_id=$1 AND message_id=$2`, b.ID, req.MessageID).Scan(&savedHash, &savedSender, &saved, &savedGeneration)
		if err == nil {
			if savedHash != fingerprint || savedSender != req.Sender {
				infoFlowError(w, 409, "message conflict")
				return
			}
			var cached infoFlowRelayResponse
			if json.Unmarshal(saved, &cached) != nil {
				infoFlowError(w, 500, "receipt")
				return
			}
			if b.IssueID.Valid && (savedGeneration != b.Generation || resp.Binding != nil && (resp.Binding.Private != nil && resp.Binding.Private.State != "active" || resp.Binding.Group != nil && resp.Binding.Group.AssignmentVersion != b.AssignmentVersion)) {
				writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true, ConversationMode: "issue", Notice: "该消息已处理，任务负责人或绑定已变化，不会重复执行"})
				return
			}
			// Do not replay old binding metadata after a switch or removal.
			if resp.Binding != nil {
				cached.Binding = resp.Binding
			}
			cached.CanComment = true
			cached.ConversationMode = resp.ConversationMode
			{
				cached.CanReconcileGroup = false
				if resp.Binding == nil {
					previous := cached.Binding
					cached.Binding = nil
					cached.CanComment = false
					// A successful unbind notice still names the removed task,
					// not another binding that happens to remain in the group.
					// Refresh its workspace-scoped metadata before exposing a link.
					if req.Operation == "unbind" && previous != nil {
						if issueID, e := util.ParseUUID(previous.IssueID); e == nil {
							old := b
							old.IssueID = issueID
							cached.Binding = sh.infoFlowBindingView(r.Context(), old, req.WorkspaceID, true)
							if cached.Binding != nil {
								cached.Binding.Group = nil
							}
						}
					}
					if req.Operation == "bind" || req.Operation == "create" {
						cached.Notice = "任务操作已处理，该任务绑定已解除，不会重复执行"
					}
				}
			}
			if resp.Binding != nil && resp.Binding.Group != nil {
				cached.CanReconcileGroup = canManage
				cached.CanComment = resp.Binding.Group.State == "active"
				if req.Operation == "bind" || req.Operation == "create" {
					cached.Notice = "群聊任务绑定已启用"
					if !cached.CanComment {
						cached.Notice = resp.Binding.Group.Reason
					}
				}
			}
			writeJSON(w, 200, cached)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			infoFlowError(w, 500, "receipt")
			return
		}
	}
	// A new association may use the target leader's ownership. Repeating an
	// existing binding remains a management operation even though it preserves
	// the binder, generation and presence lease instead of replacing them.
	if req.Operation != "message" && !canManage && !(req.Conversation.Kind == "group" && req.Operation == "bind" && !b.IssueID.Valid) {
		resp.Notice = "只有智能体所有者、工作区管理员或当前绑定人可以修改任务绑定"
		writeJSON(w, 200, resp)
		return
	}
	if req.Operation == "message" || req.Operation == "unbind" {
		// Receipt replay above wins over version changes. An uncommitted input,
		// however, must never silently follow a rebind or become unbound Chat.
		if req.ExpectedBindingID == "" || req.ExpectedGeneration == "" || !b.IssueID.Valid {
			infoFlowError(w, 409, "binding version required")
			return
		}
		invalid := &infoFlowCapture{}
		expectedID, ok := parseUUIDOrBadRequest(invalid, req.ExpectedBindingID, "expected_binding_id")
		if !ok {
			infoFlowError(w, 400, "invalid binding id")
			return
		}
		expectedGeneration, ok := parseUUIDOrBadRequest(invalid, req.ExpectedGeneration, "expected_generation")
		if !ok {
			infoFlowError(w, 400, "invalid binding generation")
			return
		}
		if expectedID != b.ID || expectedGeneration != b.Generation {
			infoFlowError(w, 409, "binding changed")
			return
		}
	}
	if req.Conversation.Kind == "private" && req.Operation == "message" && (resp.Binding == nil || resp.Binding.Private == nil || resp.Binding.Private.State != "active") {
		resp.CanComment = false
		resp.Notice = "私聊任务绑定已失效，请重新绑定"
		if resp.Binding != nil && resp.Binding.Private != nil {
			resp.Notice = resp.Binding.Private.Reason
		}
		writeJSON(w, 200, resp)
		return
	}
	if req.Conversation.Kind == "group" && req.Operation == "message" && resp.Binding != nil {
		g := resp.Binding.Group
		if g == nil || g.State != "active" {
			resp.CanComment = false
			resp.Notice = "等待确认队长或负责智能体的机器人已入群"
			if g != nil && g.Reason != "" {
				resp.Notice = g.Reason
			}
			writeJSON(w, 200, resp)
			return
		}
		if !slices.Contains(g.AgentIDs, strings.ToLower(req.AgentID)) {
			resp.CanComment = false
			resp.Notice = "该机器人不属于任务负责人或当前小队，请联系队长机器人"
			writeJSON(w, 200, resp)
			return
		}
	}
	canInvoke := sh.canInvokeAgent(r.Context(), agent, "member", uuidToString(user.ID), "", req.WorkspaceID)
	if req.Operation == "create" && !canInvoke {
		resp.Notice = "没有智能体权限，无法创建并调用任务，请向[" + sh.infoFlowOwner(r.Context(), req.WorkspaceID, &agent) + "]申请智能体权限"
		writeJSON(w, 200, resp)
		return
	}
	if req.HasAttachments && len(req.Attachments) == 0 {
		infoFlowError(w, 400, "attachment bytes required")
		return
	}
	if req.Operation == "unbind" && !b.IssueID.Valid {
		resp.Notice = "当前对话没有绑定任务"
		writeJSON(w, 200, resp)
		return
	}
	if len(req.Attachments) > 0 && req.Operation != "message" && req.Operation != "create" {
		infoFlowError(w, 400, "attachments require message/create")
		return
	}
	if !b.ID.Valid {
		b.Kind = req.Conversation.Kind
		b.ID, b.Generation = dbid.NewV7(), dbid.NewV7()
		_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_issue_binding(id,robot_id,workspace_id,agent_id,conversation_kind,conversation_id,generation) VALUES($1,$2,$3,$4,$5,$6,$7)`, b.ID, req.RobotID, req.WorkspaceID, req.AgentID, req.Conversation.Kind, req.Conversation.ID, b.Generation)
		if err != nil {
			infoFlowError(w, 500, "binding")
			return
		}
	}
	// Acked output echoes must never create another human comment or task.
	var echo bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_delivery WHERE binding_id=$1 AND (external_message_id=$2 OR $2=ANY(external_message_ids)))`, b.ID, req.MessageID).Scan(&echo)
	if err != nil {
		infoFlowError(w, 500, "echo")
		return
	}
	if echo {
		writeJSON(w, 200, infoFlowRelayResponse{Handled: true, Silent: true})
		return
	}
	var commentID, taskID pgtype.UUID
	previousBindingView := resp.Binding
	previousIssueID := b.IssueID
	previousBinding := b
	var uploads []string
	committed := false
	defer func() {
		if !committed && len(uploads) > 0 {
			sh.deleteS3Objects(context.WithoutCancel(r.Context()), uploads)
		}
	}()
	memberReq := infoFlowMemberRequest(r.Context(), uuidToString(user.ID), req.WorkspaceID, "", nil)
	groupRepeat := false
	switch req.Operation {
	case "bind":
		previouslyBound := b.IssueID.Valid
		capture := &infoFlowCapture{}
		ref := req.IssueRef
		if ref == "" {
			ref, _ = infoFlowExplicitRef(req)
		}
		issue, ok := sh.loadIssueForUser(capture, memberReq, ref)
		if !ok {
			resp.Notice = "未找到该工作区中的任务，请检查任务编号后重新绑定"
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if req.Conversation.Kind == "private" {
			if err = infoFlowPrivateWebLock(r.Context(), tx, issue.ID, user.ID); err != nil {
				infoFlowError(w, 500, "private issue claim")
				return
			}
			var elsewhere bool
			if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='private' AND issue_id=$1 AND id<>$2)`, issue.ID, b.ID).Scan(&elsewhere); err != nil {
				infoFlowError(w, 500, "private issue claim")
				return
			}
			if elsewhere {
				resp.CanComment, resp.Binding = false, nil
				resp.Notice = "该任务已绑定其他私聊，请先解除原绑定"
				writeJSON(w, 200, resp)
				return
			}
		}
		if req.Conversation.Kind == "group" {
			// Serialize claims from different groups before taking issue row
			// locks. The unique index is a second, independent safety boundary.
			if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "infoflow-group-issue:"+uuidToString(issue.ID)); err != nil {
				infoFlowError(w, 500, "issue claim")
				return
			}
			var elsewhere bool
			if pending, e := infoFlowWebReserved(r.Context(), tx, issue.ID); e != nil {
				infoFlowError(w, 500, "web binding reservation")
				return
			} else if pending {
				resp.CanComment, resp.Binding = false, nil
				resp.Notice = "网页群聊绑定正在确认，请等待完成后再使用群内绑定指令"
				writeJSON(w, 200, resp)
				return
			}
			if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind='group' AND issue_id=$1 AND id<>$2)`, issue.ID, b.ID).Scan(&elsewhere); err != nil {
				infoFlowError(w, 500, "issue claim")
				return
			}
			if elsewhere {
				resp.CanComment = false
				resp.Binding = nil
				resp.Notice = "该任务已绑定其他群聊，请先解除原绑定"
				writeJSON(w, 200, resp)
				return
			}
			groupRepeat = previouslyBound && b.IssueID == issue.ID
		}
		// Fence issue transfer/deletion through the binding commit. The loader
		// alone is a snapshot and can succeed just before a transfer commits.
		var assigneeType, assigneeID string
		var assignmentVersion int64
		if err = tx.QueryRow(r.Context(), `SELECT COALESCE(assignee_type,''),COALESCE(assignee_id::text,''),infoflow_assignment_version FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, issue.ID, req.WorkspaceID).Scan(&assigneeType, &assigneeID, &assignmentVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				resp.Notice = "任务已移出该工作区，请重新选择任务"
				writeJSON(w, 200, resp)
			} else {
				infoFlowError(w, 500, "issue lock")
			}
			return
		}
		if req.Conversation.Kind == "private" && (assigneeType != "agent" || !strings.EqualFold(assigneeID, req.AgentID)) {
			resp.CanComment, resp.Binding = false, nil
			resp.Notice = "私聊绑定仅支持由当前机器人对应的单独智能体负责的任务，请改用群聊或负责智能体的机器人"
			writeJSON(w, 200, resp)
			return
		}
		if !groupRepeat && (b.IssueID != issue.ID || b.AssignmentVersion != assignmentVersion) {
			b.Generation = dbid.NewV7()
		}
		b.IssueID = issue.ID
		if !groupRepeat {
			b.AssignmentVersion = assignmentVersion
		}
		resp.Notice = "已绑定任务"
		if req.Conversation.Kind == "group" {
			t, reason, e := sh.infoFlowTopology(r.Context(), b.IssueID, req.WorkspaceID)
			if e != nil {
				infoFlowError(w, 500, "topology")
				return
			}
			if reason != "" {
				resp.CanComment = false
				resp.Notice = reason
				writeJSON(w, 200, resp)
				return
			}
			leader, e := sh.Queries.GetAgent(r.Context(), parseUUID(t.LeaderAgentID))
			if e != nil {
				infoFlowError(w, 500, "leader")
				return
			}
			if !previouslyBound && uuidToString(leader.OwnerID) != uuidToString(user.ID) && role != "owner" && role != "admin" {
				resp.CanComment = false
				resp.Notice = "只有任务负责人、队长所有者、工作区管理员或当前绑定人可以修改群聊绑定"
				writeJSON(w, 200, resp)
				return
			}
			if !groupRepeat {
				b.GroupTopology, _ = json.Marshal(t)
			}
		}
		b.BinderID = user.ID
	case "unbind":
		b.IssueID = pgtype.UUID{}
		b.Generation = dbid.NewV7()
		b.BinderID = pgtype.UUID{}
		resp.Notice = "已解除任务绑定"
	case "message", "create":
		content := strings.TrimSpace(sanitizeNullBytes(req.Content))
		// Strip external structured actor references. Only the configured agent
		// gets a server-authored mention; arbitrary member/squad/all mentions are
		// inert text and cannot cause notifications or agent dispatch.
		content = infoFlowSanitizeMentions(content)
		if content == "" && len(req.Attachments) > 0 {
			content = "[附件]"
		}
		if content == "" {
			resp.Notice = "请输入任务内容"
			writeJSON(w, http.StatusOK, resp)
			return
		}
		attachmentIDs, urls, uploadErr := sh.infoFlowUploadAttachments(memberReq, req.Attachments, b.IssueID, req.Operation == "create")
		uploads = append(uploads, urls...)
		if uploadErr != nil {
			status := 400
			if errors.Is(uploadErr, errInfoFlowMediaUpload) {
				status = http.StatusServiceUnavailable
			}
			infoFlowError(w, status, "upload failed")
			return
		}
		capture := &infoFlowCapture{}
		if req.Operation == "create" {
			title := strings.SplitN(content, "\n", 2)[0]
			if len([]rune(title)) > 200 {
				title = string([]rune(title)[:200])
			}
			body, _ := json.Marshal(map[string]any{"title": title, "description": content, "assignee_type": "agent", "assignee_id": req.AgentID, "attachment_ids": attachmentIDs})
			sh.CreateIssue(capture, infoFlowMemberRequest(r.Context(), uuidToString(user.ID), req.WorkspaceID, "", body))
			if capture.status != 201 {
				if capture.status >= 400 && capture.status < 500 {
					resp.Notice = "无法创建任务，请检查任务内容、重复任务和智能体配置"
					writeJSON(w, http.StatusOK, resp)
				} else {
					infoFlowError(w, http.StatusInternalServerError, "create failed")
				}
				return
			}
			var out IssueResponse
			if json.Unmarshal(capture.Bytes(), &out) != nil {
				infoFlowError(w, 500, "create response")
				return
			}
			b.IssueID = parseUUID(out.ID)
			if err = tx.QueryRow(r.Context(), `SELECT infoflow_assignment_version FROM issue WHERE id=$1 AND workspace_id=$2 FOR SHARE`, b.IssueID, req.WorkspaceID).Scan(&b.AssignmentVersion); err != nil {
				infoFlowError(w, 500, "created issue version")
				return
			}
			b.Generation = dbid.NewV7()
			b.BinderID = user.ID
			err = tx.QueryRow(r.Context(), `SELECT id FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, b.IssueID, agent.ID).Scan(&taskID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				infoFlowError(w, 500, "task origin")
				return
			}
			resp.Notice = "已创建并绑定任务"
		} else {
			parent, err := sh.infoFlowQuote(r.Context(), b, req.ReplyToMessageID)
			if err != nil {
				infoFlowError(w, 409, "unknown or stale reply")
				return
			}
			mention := infoFlowNamedMention("agent", uuidToString(agent.ID), agent.Name)
			// Preserve the issue's squad-leader dispatch semantics. Direct
			// mentions of other members and single-agent assignments stay agent
			// invocations. The issue is already locked above; lock the squad too
			// so a leader change cannot race the authored mention.
			var squadID pgtype.UUID
			e := tx.QueryRow(r.Context(), `SELECT s.id FROM issue i JOIN squad s ON i.assignee_type='squad' AND i.assignee_id=s.id AND s.workspace_id=i.workspace_id WHERE i.id=$1 AND i.workspace_id=$2 AND s.leader_id=$3 AND s.archived_at IS NULL FOR SHARE OF s`, b.IssueID, req.WorkspaceID, agent.ID).Scan(&squadID)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				infoFlowError(w, 500, "squad dispatch")
				return
			}
			if squadID.Valid {
				mention = infoFlowNamedMention("squad", uuidToString(squadID), agent.Name)
			}
			body := map[string]any{"content": mention + "\n\n" + content, "attachment_ids": attachmentIDs}
			// A concurrent NEW grant must not change the already-returned
			// no-invoke decision into a run during the handler's second check.
			if !canInvoke {
				suppressed := []string{req.AgentID}
				if resp.Binding != nil && resp.Binding.Group != nil {
					suppressed = append(suppressed, resp.Binding.Group.AgentIDs...)
				}
				if parent.Valid {
					var author string
					e := tx.QueryRow(r.Context(), `SELECT author_id::text FROM comment WHERE id=$1 AND issue_id=$2 AND author_type='agent'`, parent, b.IssueID).Scan(&author)
					if e == nil {
						suppressed = append(suppressed, author)
					} else if !errors.Is(e, pgx.ErrNoRows) {
						infoFlowError(w, 500, "parent author")
						return
					}
				}
				body["suppress_agent_ids"] = suppressed
			}
			if parent.Valid {
				body["parent_id"] = uuidToString(parent)
			}
			encoded, _ := json.Marshal(body)
			sh.CreateComment(capture, infoFlowMemberRequest(r.Context(), uuidToString(user.ID), req.WorkspaceID, uuidToString(b.IssueID), encoded))
			if capture.status != 201 {
				if capture.status >= 400 && capture.status < 500 {
					resp.Notice = "无法新增评论，请检查任务绑定后重试"
					writeJSON(w, http.StatusOK, resp)
				} else {
					infoFlowError(w, http.StatusInternalServerError, "comment failed")
				}
				return
			}
			var out CommentResponse
			if json.Unmarshal(capture.Bytes(), &out) != nil {
				infoFlowError(w, 500, "comment response")
				return
			}
			commentID = parseUUID(out.ID)
			resp.CommentID = out.ID
			if !canInvoke {
				resp.Notice = "没有智能体权限，当前仅新增评论，但无法调用智能体，请向[" + sh.infoFlowOwner(r.Context(), req.WorkspaceID, &agent) + "]申请智能体权限"
			}
		}
	}
	if req.Operation != "message" && !groupRepeat {
		if req.Operation == "bind" || req.Operation == "unbind" {
			changedIssue := b.IssueID
			if req.Operation == "unbind" {
				changedIssue = previousIssueID
			}
			if changedIssue.Valid {
				if err = infoFlowInvalidateWebRequest(r.Context(), tx, req.Conversation.Kind, infoFlowWebActor{req.WorkspaceID, changedIssue, user.ID}, "binding_changed"); err != nil {
					infoFlowError(w, 500, "request invalidation")
					return
				}
			}
		}
		if req.Conversation.Kind == "private" && (req.Operation == "bind" || req.Operation == "create") && req.RobotChatID != "" {
			b.RobotChatID, b.RobotName = req.RobotChatID, strings.TrimSpace(req.RobotName)
		}
		_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET sync_enabled=CASE WHEN issue_id IS DISTINCT FROM $2 THEN true ELSE sync_enabled END,issue_id=$2,binder_id=$3,generation=$4,bound_at=CASE WHEN generation<>$4 THEN clock_timestamp() ELSE bound_at END,assignment_version=$5,robot_chat_id=$6,robot_name=$7 WHERE id=$1`, b.ID, b.IssueID, b.BinderID, b.Generation, b.AssignmentVersion, b.RobotChatID, b.RobotName)
		if err != nil {
			infoFlowError(w, 500, "binding update")
			return
		}
		if req.Conversation.Kind == "group" {
			if req.Operation == "create" {
				t, reason, e := sh.infoFlowTopology(r.Context(), b.IssueID, req.WorkspaceID)
				if e != nil || reason != "" {
					infoFlowError(w, 409, "topology")
					return
				}
				b.GroupTopology, _ = json.Marshal(t)
			}
			_, err = tx.Exec(r.Context(), `UPDATE infoflow_issue_binding SET group_topology=$2,group_state='pending',leader_robot_id=NULL,leader_verified_at=NULL WHERE id=$1`, b.ID, b.GroupTopology)
			if err != nil {
				infoFlowError(w, 500, "group update")
				return
			}
			b.GroupState = "pending"
			b.LeaderRobotID = ""
			b.LeaderVerifiedAt = pgtype.Timestamptz{}
			if req.Operation != "unbind" {
				resp.Notice = "任务已关联，等待确认队长或负责智能体的机器人已入群"
				if req.Operation == "create" {
					resp.Notice = "已创建任务，等待确认负责智能体的机器人已入群后启用群聊绑定"
				}
				resp.CanComment = false
				resp.CanReconcileGroup = true
			}
		}
	}
	if req.Operation == "unbind" {
		previousBinding.BinderID = user.ID
		if err = infoFlowEnqueueNotice(r.Context(), tx, req.infoFlowScope, req.Conversation, previousBinding, "unbound"); err != nil {
			infoFlowError(w, 500, "binding notice")
			return
		}
		resp.SuppressIMNotice = true
	} else if req.Conversation.Kind == "private" && (req.Operation == "bind" || req.Operation == "create") {
		if err = infoFlowEnqueueNotice(r.Context(), tx, req.infoFlowScope, req.Conversation, b, "bound"); err != nil {
			infoFlowError(w, 500, "binding notice")
			return
		}
		resp.SuppressIMNotice = true
	}
	resp.Binding = sh.infoFlowBindingView(r.Context(), b, req.WorkspaceID, true)
	if groupRepeat && resp.Binding != nil && resp.Binding.Group != nil {
		resp.CanComment = resp.Binding.Group.State == "active"
		resp.CanReconcileGroup = canManage
		resp.Notice = "群聊任务绑定已启用"
		if !resp.CanComment {
			resp.Notice = resp.Binding.Group.Reason
		}
	}
	resp.ConversationMode = infoFlowConversationMode(b)
	if req.Operation == "unbind" {
		resp.Binding = previousBindingView
		{
			var remaining bool
			if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM infoflow_issue_binding WHERE conversation_kind=$2 AND conversation_id=$1 AND issue_id IS NOT NULL AND ($2='group' OR (robot_id=$3 AND workspace_id=$4 AND agent_id::text=lower($5))))`, req.Conversation.ID, req.Conversation.Kind, req.RobotID, req.WorkspaceID, req.AgentID).Scan(&remaining); err != nil {
				infoFlowError(w, 500, "remaining bindings")
				return
			}
			if remaining {
				resp.ConversationMode = "issue"
			}
		}
	}
	encoded, _ := json.Marshal(resp)
	_, err = tx.Exec(r.Context(), `INSERT INTO infoflow_issue_source(id,binding_id,generation,message_id,sender,user_id,request_hash,issue_id,comment_id,task_id,response,notify_users) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, dbid.NewV7(), b.ID, b.Generation, req.MessageID, req.Sender, user.ID, fingerprint, b.IssueID, commentID, taskID, encoded, notifyUsers)
	if err != nil {
		infoFlowError(w, 500, "source mapping")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		infoFlowError(w, 500, "commit")
		return
	}
	committed = true
	flush()
	writeJSON(w, 200, resp)
}

func infoFlowValidGroupID(id string) bool {
	if id == "" || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	v, err := strconv.ParseUint(id, 10, 63)
	return err == nil && v > 0
}

// Replacing a nested markdown label can reassemble a new syntactically valid
// mention. Neutralize every remaining protocol prefix after label extraction;
// unlike repeated regex replacements, this remains linear for hostile nesting.
func infoFlowSanitizeMentions(content string) string {
	return strings.ReplaceAll(util.MentionRe.ReplaceAllString(content, "$1"), "mention://", "mention: //")
}

func (h *Handler) infoFlowQuote(ctx context.Context, b infoFlowBinding, messageID string) (pgtype.UUID, error) {
	if messageID == "" {
		return pgtype.UUID{}, nil
	}
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx, `SELECT comment_id FROM infoflow_issue_source WHERE binding_id=$1 AND generation=$2 AND message_id=$3 AND issue_id=$4 AND comment_id IS NOT NULL
		UNION ALL SELECT d.comment_id FROM infoflow_issue_delivery d JOIN comment c ON c.id=d.comment_id WHERE d.binding_id=$1 AND d.generation=$2 AND (d.external_message_id=$3 OR $3=ANY(d.external_message_ids)) AND c.issue_id=$4 LIMIT 1`, b.ID, b.Generation, messageID, b.IssueID).Scan(&id)
	// A quote of a pre-binding/external message remains a top-level comment;
	// no caller-supplied identifier is ever treated as a platform comment id.
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, nil
	}
	return id, err
}
