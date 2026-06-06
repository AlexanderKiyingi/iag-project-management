package consumer

import (
	"context"
	"fmt"
	"strings"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Handler) resolveContractProject(ctx context.Context, data map[string]any) (owner, projectID string, ok bool) {
	owner = stringField(data, "workspaceOwnerUserId")
	projectID = strings.TrimSpace(stringField(data, "pmProjectId"))
	if projectID == "" {
		projectID = strings.TrimSpace(stringField(data, "zone"))
	}
	if projectID == "" {
		return "", "", false
	}
	if owner != "" {
		return owner, projectID, true
	}
	owner, ok = h.Repo.FindOwnerByProjectRef(ctx, projectID)
	return owner, projectID, ok
}

func upsertProjectContractLink(d *models.Document, projectID string, link models.ProjectContractLink) bool {
	if d.Projects == nil {
		return false
	}
	p, ok := d.Projects[projectID]
	if !ok {
		for id, proj := range d.Projects {
			if strings.EqualFold(proj.Code, projectID) || strings.EqualFold(proj.ID, projectID) {
				projectID = id
				p = proj
				ok = true
				break
			}
		}
	}
	if !ok {
		return false
	}
	for i, existing := range p.LinkedContracts {
		if existing.No == link.No {
			p.LinkedContracts[i] = link
			d.Projects[projectID] = p
			return true
		}
	}
	p.LinkedContracts = append(p.LinkedContracts, link)
	d.Projects[projectID] = p
	return true
}

func (h *Handler) linkContractOnProject(ctx context.Context, env platformevents.Envelope, auditAction string) error {
	owner, projectID, ok := h.resolveContractProject(ctx, env.Data)
	if !ok {
		return nil
	}
	no := stringField(env.Data, "no")
	name := stringField(env.Data, "name")
	status := stringField(env.Data, "status")
	zone := stringField(env.Data, "zone")
	link := models.ProjectContractLink{
		No:       no,
		Name:     name,
		Status:   status,
		Zone:     zone,
		LinkedAt: env.Time,
	}
	_, err := h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
		if upsertProjectContractLink(d, projectID, link) {
			d.Notifications = append(d.Notifications, models.WorkspaceNotification{
				ID:    nextNotificationID(d),
				Icon:  "contract",
				Title: fmt.Sprintf("Contract %s linked to project", no),
				Meta:  env.Time,
				Body:  fmt.Sprintf("%s linked to project %s", name, projectID),
			})
		}
		models.AppendAudit(d, "contracts", auditAction,
			fmt.Sprintf("Contract %s (%s) linked to project %s", no, name, projectID), nil)
		return nil
	})
	return err
}

func updateLinkedContractStatus(d *models.Document, no, status string) bool {
	changed := false
	for id, p := range d.Projects {
		for i, link := range p.LinkedContracts {
			if link.No != no {
				continue
			}
			p.LinkedContracts[i].Status = status
			d.Projects[id] = p
			changed = true
		}
	}
	return changed
}

func removeLinkedContract(d *models.Document, no string) bool {
	changed := false
	for id, p := range d.Projects {
		kept := p.LinkedContracts[:0]
		for _, link := range p.LinkedContracts {
			if link.No == no {
				changed = true
				continue
			}
			kept = append(kept, link)
		}
		if changed {
			p.LinkedContracts = kept
			d.Projects[id] = p
		}
	}
	return changed
}
