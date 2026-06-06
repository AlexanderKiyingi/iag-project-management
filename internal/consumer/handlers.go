// Package consumer subscribes to iag.commercial and applies upstream domain
// events (contract, procurement, auth) to PM workspaces. Each handler is
// idempotent at the workspace level (via the platform-go dedupe table) and
// goes through workspace.Service.Mutate to inherit optimistic-lock retry.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"

	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/financeclient"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
)

// Event types PM listens for on iag.commercial.
const (
	TypeRequisitionApproved   = "procurement.requisition.approved"
	TypeRequisitionRejected   = "procurement.requisition.rejected"
	TypeContractCreated       = "contracts.contract.created"
	TypeContractUpdated       = "contracts.contract.updated"
	TypeContractDeleted       = "contracts.contract.deleted"
	TypeContractStatusChanged = "contracts.contract.status_changed"
	TypeAssistanceRequested   = "contracts.assistance.requested"
	TypeMilestoneDueSoon      = "contracts.milestone.due_soon"
	TypeUserDeactivated       = "auth.user.deactivated"
	TypeUserReactivated       = "auth.user.reactivated"
	TypeUserUpdated           = "auth.user.updated"
	TypeUserDeleted           = "auth.user.deleted"
	TypeOrgMemberAdded        = "users.org.member_added"
	TypeOrgMemberRemoved      = "users.org.member_removed"
)

// Handler routes envelopes to the right per-type handler. Unknown types are
// silently ignored — PM is one consumer among many on iag.commercial.
type Handler struct {
	Svc     *workspace.Service
	Repo    *store.Repository
	Finance *financeclient.Client
}

// Handle implements platformevents.Handler. Events sourced from PM itself
// are skipped before any work happens — they reach this consumer because PM
// publishes and subscribes to the same topic, but they describe state PM
// already owns.
func (h *Handler) Handle(ctx context.Context, env platformevents.Envelope) error {
	if env.Source == events.Source {
		return nil
	}
	switch env.Type {
	case TypeRequisitionApproved:
		return h.handleRequisition(ctx, env, "approved")
	case TypeRequisitionRejected:
		return h.handleRequisition(ctx, env, "rejected")
	case TypeContractCreated:
		return h.handleContractCreated(ctx, env)
	case TypeContractUpdated, TypeContractDeleted:
		return h.handleContractLifecycle(ctx, env)
	case TypeContractStatusChanged:
		return h.handleContractStatusChanged(ctx, env)
	case TypeAssistanceRequested:
		return h.handleAssistanceRequested(ctx, env)
	case TypeMilestoneDueSoon:
		return h.handleMilestoneDueSoon(ctx, env)
	case TypeUserDeactivated, TypeUserDeleted:
		return h.handleUserDeactivated(ctx, env)
	case TypeUserReactivated:
		return h.handleUserReactivated(ctx, env)
	case TypeUserUpdated:
		return h.handleUserUpdated(ctx, env)
	case TypeOrgMemberAdded:
		return h.handleOrgMemberAdded(ctx, env)
	case TypeOrgMemberRemoved:
		return h.handleOrgMemberRemoved(ctx, env)
	default:
		return nil
	}
}

func (h *Handler) handleRequisition(ctx context.Context, env platformevents.Envelope, outcome string) error {
	owner := stringField(env.Data, "workspaceOwnerUserId")
	reqIDStr := stringField(env.Data, "requisitionId")
	if owner == "" || reqIDStr == "" {
		return platformevents.Permanent(fmt.Errorf("requisition event missing workspaceOwnerUserId or requisitionId"))
	}
	reqID, ok := parseIntID(reqIDStr)
	if !ok {
		return platformevents.Permanent(fmt.Errorf("requisition event has non-numeric requisitionId %q", reqIDStr))
	}

	actor := stringField(env.Data, "approvedBy")
	if actor == "" {
		actor = "procurement"
	}
	ts := stringField(env.Data, "approvedAt")
	if ts == "" {
		ts = stringField(env.Data, "rejectedAt")
	}
	if ts == "" {
		ts = env.Time
	}

	found := false
	_, err := h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
		for i := range d.Requisitions {
			if d.Requisitions[i].ID != reqID {
				continue
			}
			found = true
			d.Requisitions[i].Status = outcome
			switch outcome {
			case "approved":
				d.Requisitions[i].ApprovedBy = strPtr(actor)
				d.Requisitions[i].ApprovedAt = strPtr(ts)
			case "rejected":
				d.Requisitions[i].RejectedAt = strPtr(ts)
			}
			models.AppendAudit(d, actor, "procurement.requisition."+outcome,
				fmt.Sprintf("Requisition #%d %s by %s", reqID, outcome, actor), nil)
			return nil
		}
		slog.Info("requisition not found for upstream event", "owner", owner, "reqId", reqID, "outcome", outcome)
		return nil
	})
	if err != nil {
		return err
	}
	if found && outcome == "approved" {
		h.bookApprovedRequisitionAP(ctx, owner, reqID)
	}
	return nil
}

func (h *Handler) handleContractCreated(ctx context.Context, env platformevents.Envelope) error {
	if _, _, ok := h.resolveContractProject(ctx, env.Data); ok {
		return h.linkContractOnProject(ctx, env, "contracts.contract.created")
	}
	owner := stringField(env.Data, "workspaceOwnerUserId")
	if owner == "" {
		return nil
	}
	no := stringField(env.Data, "no")
	name := stringField(env.Data, "name")
	_, err := h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
		models.AppendAudit(d, "contracts", "contracts.contract.created",
			fmt.Sprintf("Contract %s (%s) created", no, name), nil)
		return nil
	})
	return err
}

// handleContractLifecycle logs contracts.contract.updated and .deleted as
// audit entries on every workspace. Contract events don't carry a workspace
// identifier, so the broadcast is unconditional — auditing on every workspace
// gives admins a record without adding UI notifications (these events are
// high-frequency).
func (h *Handler) handleContractLifecycle(ctx context.Context, env platformevents.Envelope) error {
	no := stringField(env.Data, "no")
	name := stringField(env.Data, "name")
	var action string
	switch env.Type {
	case TypeContractUpdated:
		action = "updated"
	case TypeContractDeleted:
		action = "deleted"
	default:
		return nil
	}
	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			if action == "deleted" {
				removeLinkedContract(d, no)
			}
			models.AppendAudit(d, "contracts", "contracts.contract."+action,
				fmt.Sprintf("Contract %s (%s) %s", no, name, action), nil)
			return nil
		}); err != nil {
			slog.Warn("contract lifecycle consumer: mutate failed", "owner", ws.OwnerUserID, "action", action, "err", err)
		}
	}
	return nil
}

// handleContractStatusChanged adds a notification on every workspace whose
// Members include the contract owner (cs field). Falls back to audit-only if
// no owner is identified.
func (h *Handler) handleContractStatusChanged(ctx context.Context, env platformevents.Envelope) error {
	no := stringField(env.Data, "no")
	name := stringField(env.Data, "name")
	status := stringField(env.Data, "status")
	previousStatus := stringField(env.Data, "previousStatus")
	owner := strings.ToLower(stringField(env.Data, "cs"))

	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			slog.Warn("contract status consumer: skip unparsable workspace", "owner", ws.OwnerUserID, "err", err)
			continue
		}
		matched := owner != "" && workspaceHasMember(&doc, owner)
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			if updateLinkedContractStatus(d, no, status) && matched {
				d.Notifications = append(d.Notifications, models.WorkspaceNotification{
					ID:    nextNotificationID(d),
					Icon:  "contract",
					Title: fmt.Sprintf("Contract %s status: %s", no, status),
					Meta:  env.Time,
					Body:  fmt.Sprintf("%s — %s → %s", name, previousStatus, status),
				})
			} else if matched {
				d.Notifications = append(d.Notifications, models.WorkspaceNotification{
					ID:    nextNotificationID(d),
					Icon:  "contract",
					Title: fmt.Sprintf("Contract %s status: %s", no, status),
					Meta:  env.Time,
					Body:  fmt.Sprintf("%s — %s → %s", name, previousStatus, status),
				})
			}
			models.AppendAudit(d, "contracts", "contracts.contract.status_changed",
				fmt.Sprintf("Contract %s (%s): %s → %s", no, name, previousStatus, status), nil)
			return nil
		}); err != nil {
			slog.Warn("contract status consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

// handleAssistanceRequested adds a notification to workspaces whose Members
// include the requestor (`from`). This is the most signal-rich contract event
// — it implies someone needs another team's involvement, so a UI nudge helps.
func (h *Handler) handleAssistanceRequested(ctx context.Context, env platformevents.Envelope) error {
	from := strings.ToLower(stringField(env.Data, "from"))
	if from == "" {
		return nil
	}
	text := stringField(env.Data, "text")
	at := stringField(env.Data, "at")
	if at == "" {
		at = env.Time
	}

	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			continue
		}
		if !workspaceHasMember(&doc, from) {
			continue
		}
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			d.Notifications = append(d.Notifications, models.WorkspaceNotification{
				ID:      nextNotificationID(d),
				Icon:    "help-circle",
				Title:   "Assistance requested",
				Meta:    at,
				Body:    text,
				Mention: true,
			})
			models.AppendAudit(d, "contracts", "contracts.assistance.requested",
				fmt.Sprintf("Assistance requested by %s", from), nil)
			return nil
		}); err != nil {
			slog.Warn("assistance-requested consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

// handleMilestoneDueSoon adds a workspace notification to every workspace whose
// Members include the milestone owner. Owner is matched by Member.Initials or
// Member.Name; matching is case-insensitive.
func (h *Handler) handleMilestoneDueSoon(ctx context.Context, env platformevents.Envelope) error {
	owner := strings.ToLower(stringField(env.Data, "owner"))
	if owner == "" {
		return nil
	}
	title := stringField(env.Data, "title")
	due := stringField(env.Data, "due")

	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			slog.Warn("milestone consumer: skip unparsable workspace", "owner", ws.OwnerUserID, "err", err)
			continue
		}
		if !workspaceHasMember(&doc, owner) {
			continue
		}
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			d.Notifications = append(d.Notifications, models.WorkspaceNotification{
				ID:    nextNotificationID(d),
				Icon:  "contract",
				Title: "Milestone due soon",
				Meta:  due,
				Body:  fmt.Sprintf("%s — %s", title, due),
			})
			models.AppendAudit(d, "contracts", "contracts.milestone.due_soon",
				fmt.Sprintf("Milestone %q due %s notified to %s", title, due, owner), nil)
			return nil
		}); err != nil {
			slog.Warn("milestone consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

// handleUserDeactivated marks the user inactive in every workspace where they
// appear as a Member and clears them from task assignments. Match strategy
// (in order, case-insensitive): UserID (canonical), Email, Name, Initials.
// Workspace mutations are best-effort; per-workspace failures log but do not
// fail the consumer (so one bad workspace can't block the bus).
func (h *Handler) handleUserDeactivated(ctx context.Context, env platformevents.Envelope) error {
	userID := stringField(env.Data, "userId")
	email := stringField(env.Data, "email")
	fullName := stringField(env.Data, "fullName")
	if userID == "" && email == "" {
		return platformevents.Permanent(fmt.Errorf("user.deactivated event missing userId and email"))
	}
	actor := stringField(env.Data, "actor")
	if actor == "" {
		actor = "auth"
	}

	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			applyUserDeactivation(d, userID, email, fullName, actor)
			return nil
		}); err != nil {
			slog.Warn("user-deactivated consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

// handleUserReactivated is the symmetric counterpart to handleUserDeactivated:
// when auth re-enables a user, flip Member.Active back to true. Task
// assignments are not restored — once unassigned, the workspace owner must
// re-assign manually (we can't know what the prior assignment was, and the
// task may have been picked up by someone else in the interim).
func (h *Handler) handleUserReactivated(ctx context.Context, env platformevents.Envelope) error {
	userID := stringField(env.Data, "userId")
	email := stringField(env.Data, "email")
	fullName := stringField(env.Data, "fullName")
	if userID == "" && email == "" && fullName == "" {
		return platformevents.Permanent(fmt.Errorf("user.reactivated event missing userId, email, and fullName"))
	}
	actor := stringField(env.Data, "actor")
	if actor == "" {
		actor = "auth"
	}
	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			applyUserReactivation(d, userID, email, fullName, actor)
			return nil
		}); err != nil {
			slog.Warn("user-reactivated consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

func applyUserReactivation(d *models.Document, userID, email, fullName, actor string) {
	userIDLower := strings.ToLower(strings.TrimSpace(userID))
	emailLower := strings.ToLower(strings.TrimSpace(email))
	nameLower := strings.ToLower(strings.TrimSpace(fullName))
	changed := false
	for i := range d.Members {
		m := &d.Members[i]
		if !memberMatches(m, userIDLower, emailLower, nameLower) {
			continue
		}
		if m.Active == nil || *m.Active {
			continue // already active
		}
		on := true
		m.Active = &on
		changed = true
	}
	if !changed {
		return
	}
	subject := email
	if subject == "" {
		subject = fullName
	}
	models.AppendAudit(d, actor, "auth.user.reactivated",
		fmt.Sprintf("Reactivated %s", subject), nil)
}

// applyUserDeactivation mutates a workspace document in place: flips Member.Active
// false for the deactivated user, clears their assignments from open tasks, and
// appends a single audit entry summarising the change. Idempotent — re-running
// produces no new mutations.
func applyUserDeactivation(d *models.Document, userID, email, fullName, actor string) {
	userIDLower := strings.ToLower(strings.TrimSpace(userID))
	emailLower := strings.ToLower(strings.TrimSpace(email))
	nameLower := strings.ToLower(strings.TrimSpace(fullName))
	matchedAny := false

	for i := range d.Members {
		m := &d.Members[i]
		if !memberMatches(m, userIDLower, emailLower, nameLower) {
			continue
		}
		if m.Active != nil && !*m.Active {
			continue // already inactive
		}
		off := false
		m.Active = &off
		matchedAny = true
	}

	cleared := 0
	for i := range d.Tasks {
		t := &d.Tasks[i]
		if t.Done {
			continue
		}
		if t.Assignee == "" {
			continue
		}
		assigneeLower := strings.ToLower(strings.TrimSpace(t.Assignee))
		if (userIDLower != "" && assigneeLower == userIDLower) ||
			(emailLower != "" && assigneeLower == emailLower) ||
			(nameLower != "" && assigneeLower == nameLower) {
			t.Assignee = ""
			cleared++
		}
	}

	if !matchedAny && cleared == 0 {
		return
	}
	subject := email
	if subject == "" {
		subject = fullName
	}
	if subject == "" {
		subject = userID
	}
	models.AppendAudit(d, actor, "auth.user.deactivated",
		fmt.Sprintf("Deactivated %s — cleared %d task assignment(s)", subject, cleared), nil)
}

// memberMatches matches a member against any of three identifiers (case-
// insensitive). UserID is the canonical match — set by addMember when a new
// user is invited. Email and name are fallbacks for legacy members whose
// records predate the UserID field.
func memberMatches(m *models.Member, userIDLower, emailLower, nameLower string) bool {
	if userIDLower != "" && strings.EqualFold(strings.TrimSpace(m.UserID), userIDLower) {
		return true
	}
	if emailLower != "" && strings.EqualFold(strings.TrimSpace(m.Email), emailLower) {
		return true
	}
	if nameLower != "" {
		if strings.EqualFold(strings.TrimSpace(m.Name), nameLower) ||
			strings.EqualFold(strings.TrimSpace(m.Initials), nameLower) {
			return true
		}
	}
	return false
}

func stringField(data map[string]any, key string) string {
	v, ok := data[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func parseIntID(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func strPtr(s string) *string { return &s }

func workspaceHasMember(d *models.Document, ownerLower string) bool {
	for _, m := range d.Members {
		if strings.EqualFold(strings.TrimSpace(m.Initials), ownerLower) ||
			strings.EqualFold(strings.TrimSpace(m.Name), ownerLower) {
			return true
		}
	}
	return false
}

func (h *Handler) handleUserUpdated(ctx context.Context, env platformevents.Envelope) error {
	userID := stringField(env.Data, "userId")
	email := stringField(env.Data, "email")
	fullName := stringField(env.Data, "fullName")
	if userID == "" {
		return platformevents.Permanent(fmt.Errorf("user.updated event missing userId"))
	}
	actor := stringField(env.Data, "actor")
	if actor == "" {
		actor = "auth"
	}
	list, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range list {
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			if applyUserIdentityUpdate(d, userID, email, fullName, actor) {
				return nil
			}
			return nil
		}); err != nil {
			slog.Warn("user-updated consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

func (h *Handler) handleOrgMemberAdded(ctx context.Context, env platformevents.Envelope) error {
	orgID := stringField(env.Data, "orgId")
	userID := stringField(env.Data, "userId")
	if orgID == "" || userID == "" {
		return platformevents.Permanent(fmt.Errorf("org.member_added missing orgId or userId"))
	}
	email := stringField(env.Data, "email")
	fullName := stringField(env.Data, "fullName")
	role := stringField(env.Data, "role")
	if role == "" {
		role = "member"
	}
	actor := stringField(env.Data, "actor")
	if actor == "" {
		actor = "iag-users"
	}

	workspaces, err := h.Repo.ListWorkspacesByOrgID(ctx, orgID)
	if err != nil {
		return err
	}
	for _, ws := range workspaces {
		if err := h.Repo.AddMember(ctx, ws.ID, userID, role); err != nil {
			slog.Warn("org member added: sql member failed", "workspace", ws.ID, "err", err)
		}
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			if applyOrgMemberAdded(d, userID, email, fullName, role, actor) {
				models.AppendAudit(d, actor, "users.org.member_added",
					fmt.Sprintf("Org member %s added to workspace team", displayName(email, fullName, userID)), nil)
			}
			return nil
		}); err != nil {
			slog.Warn("org member added: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

func (h *Handler) handleOrgMemberRemoved(ctx context.Context, env platformevents.Envelope) error {
	orgID := stringField(env.Data, "orgId")
	userID := stringField(env.Data, "userId")
	if orgID == "" || userID == "" {
		return platformevents.Permanent(fmt.Errorf("org.member_removed missing orgId or userId"))
	}
	email := stringField(env.Data, "email")
	fullName := stringField(env.Data, "fullName")
	actor := stringField(env.Data, "actor")
	if actor == "" {
		actor = "iag-users"
	}

	workspaces, err := h.Repo.ListWorkspacesByOrgID(ctx, orgID)
	if err != nil {
		return err
	}
	for _, ws := range workspaces {
		if err := h.Repo.RemoveMember(ctx, ws.ID, userID); err != nil {
			slog.Warn("org member removed: sql delete failed", "workspace", ws.ID, "err", err)
		}
		if _, err := h.Svc.Mutate(ctx, ws.OwnerUserID, func(d *models.Document) error {
			applyUserDeactivation(d, userID, email, fullName, actor)
			models.AppendAudit(d, actor, "users.org.member_removed",
				fmt.Sprintf("Org member %s removed from workspace team", displayName(email, fullName, userID)), nil)
			return nil
		}); err != nil {
			slog.Warn("org member removed: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

func applyUserIdentityUpdate(d *models.Document, userID, email, fullName, actor string) bool {
	userIDLower := strings.ToLower(strings.TrimSpace(userID))
	emailLower := strings.ToLower(strings.TrimSpace(email))
	nameLower := strings.ToLower(strings.TrimSpace(fullName))
	changed := false
	for i := range d.Members {
		m := &d.Members[i]
		if !memberMatches(m, userIDLower, emailLower, nameLower) {
			continue
		}
		if email != "" && m.Email != email {
			m.Email = email
			changed = true
		}
		if fullName != "" && m.Name != fullName {
			m.Name = fullName
			changed = true
		}
		if userID != "" && m.UserID == "" {
			m.UserID = userID
			changed = true
		}
	}
	if changed {
		subject := displayName(email, fullName, userID)
		models.AppendAudit(d, actor, "auth.user.updated",
			fmt.Sprintf("Updated profile for %s", subject), nil)
	}
	return changed
}

func applyOrgMemberAdded(d *models.Document, userID, email, fullName, role, actor string) bool {
	userIDLower := strings.ToLower(strings.TrimSpace(userID))
	for _, m := range d.Members {
		if m.UserID != "" && strings.EqualFold(m.UserID, userIDLower) {
			return false
		}
		if email != "" && m.Email != "" && strings.EqualFold(m.Email, email) {
			return false
		}
	}
	name := fullName
	if name == "" {
		name = email
	}
	active := true
	member := models.Member{
		UserID:     userID,
		Email:      email,
		Name:       name,
		Initials:   initialsFromName(name),
		Role:       role,
		AccessRole: role,
		Active:     &active,
	}
	d.Members = append(d.Members, member)
	return true
}

func initialsFromName(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	if len(parts) == 0 {
		return "??"
	}
	if len(parts) == 1 {
		r := []rune(parts[0])
		if len(r) >= 2 {
			return strings.ToUpper(string(r[0:2]))
		}
		return strings.ToUpper(string(r))
	}
	return strings.ToUpper(string([]rune(parts[0])[0]) + string([]rune(parts[len(parts)-1])[0]))
}

func displayName(email, fullName, userID string) string {
	if fullName != "" {
		return fullName
	}
	if email != "" {
		return email
	}
	return userID
}

// ApplyOrgMemberAdded adds a platform org member to a workspace document when absent.
func ApplyOrgMemberAdded(d *models.Document, userID, email, fullName, role string) bool {
	return applyOrgMemberAdded(d, userID, email, fullName, role, "iag-users")
}

func nextNotificationID(d *models.Document) int {
	max := 0
	for _, n := range d.Notifications {
		if n.ID > max {
			max = n.ID
		}
	}
	return max + 1
}
