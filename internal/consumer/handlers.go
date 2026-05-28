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

	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
)

// Event types PM listens for on iag.commercial.
const (
	TypeRequisitionApproved = "procurement.requisition.approved"
	TypeRequisitionRejected = "procurement.requisition.rejected"
	TypeContractCreated     = "contracts.contract.created"
	TypeMilestoneDueSoon    = "contracts.milestone.due_soon"
	TypeUserDeactivated     = "auth.user.deactivated"
)

// Handler routes envelopes to the right per-type handler. Unknown types are
// silently ignored — PM is one consumer among many on iag.commercial.
type Handler struct {
	Svc  *workspace.Service
	Repo *store.Repository
}

// Handle implements platformevents.Handler.
func (h *Handler) Handle(ctx context.Context, env platformevents.Envelope) error {
	switch env.Type {
	case TypeRequisitionApproved:
		return h.handleRequisition(ctx, env, "approved")
	case TypeRequisitionRejected:
		return h.handleRequisition(ctx, env, "rejected")
	case TypeContractCreated:
		return h.handleContractCreated(ctx, env)
	case TypeMilestoneDueSoon:
		return h.handleMilestoneDueSoon(ctx, env)
	case TypeUserDeactivated:
		return h.handleUserDeactivated(ctx, env)
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

	_, err := h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
		for i := range d.Requisitions {
			if d.Requisitions[i].ID != reqID {
				continue
			}
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
		// Requisition not found in workspace — not an error (could have been
		// deleted locally); audit it and move on.
		slog.Info("requisition not found for upstream event", "owner", owner, "reqId", reqID, "outcome", outcome)
		return nil
	})
	return err
}

func (h *Handler) handleContractCreated(ctx context.Context, env platformevents.Envelope) error {
	owner := stringField(env.Data, "workspaceOwnerUserId")
	if owner == "" {
		// Contract events aren't targeted at a specific workspace — skip.
		// Future: link to a project if data.projectId resolves to a known project.
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
// appear as a Member and clears them from task assignments. Match strategy:
// Email (if Member.Email present), then Name, then Initials — all
// case-insensitive. Workspace mutations are best-effort; per-workspace failures
// log but do not fail the consumer (so one bad workspace can't block the bus).
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
			applyUserDeactivation(d, email, fullName, actor)
			return nil
		}); err != nil {
			slog.Warn("user-deactivated consumer: mutate failed", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return nil
}

// applyUserDeactivation mutates a workspace document in place: flips Member.Active
// false for the deactivated user, clears their assignments from open tasks, and
// appends a single audit entry summarising the change. Idempotent — re-running
// produces no new mutations.
func applyUserDeactivation(d *models.Document, email, fullName, actor string) {
	emailLower := strings.ToLower(strings.TrimSpace(email))
	nameLower := strings.ToLower(strings.TrimSpace(fullName))
	matchedAny := false

	for i := range d.Members {
		m := &d.Members[i]
		if !memberMatches(m, emailLower, nameLower) {
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
		if (emailLower != "" && assigneeLower == emailLower) ||
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
	models.AppendAudit(d, actor, "auth.user.deactivated",
		fmt.Sprintf("Deactivated %s — cleared %d task assignment(s)", subject, cleared), nil)
}

func memberMatches(m *models.Member, emailLower, nameLower string) bool {
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

func nextNotificationID(d *models.Document) int {
	max := 0
	for _, n := range d.Notifications {
		if n.ID > max {
			max = n.ID
		}
	}
	return max + 1
}
