// Package automation runs the per-workspace rules engine inline with
// document mutations.
//
// The engine sits between the user's mutation function and the
// persist+broadcast step in workspace.Service.Mutate: it diffs the
// document before/after the user fn, fires matching triggers, and
// applies the configured actions. Because everything happens against
// the in-memory *models.Document, the rule's effects land in the same
// version bump as the originating change.
//
// Loop prevention: when an action mutates the document (assign,
// set_status, etc.), those mutations could in turn match another
// rule's trigger. The engine caps that fanout at MaxDepth iterations
// per Run call.
package automation

import (
	"fmt"
	"strings"
	"time"

	"github.com/iag/project-management/backend/internal/models"
)

const MaxDepth = 3

// Event is the change that fired a trigger.
type Event struct {
	Type     string
	TaskID   int
	Old      *models.Task    // pre-state, nil for task.created
	New      *models.Task    // post-state
	Comment  *models.TaskComment
	EntityComment *models.EntityComment
}

// NotifyHook is invoked when a rule's notify action fires. Implemented
// by the workspace service so the engine doesn't import events. The
// hook is called AFTER the document is persisted to avoid publishing
// a notification for a state that was later rolled back.
type NotifyHook func(rule models.Rule, action models.RuleAction, evt Event)

// DiffAndRun compares the pre-mutation snapshot (`prev`) to the
// post-mutation document (`doc`), generates an event stream, and runs
// matching rules. Mutations applied by actions are written back to
// `doc`. Returns the list of post-persist notify callbacks the caller
// should fire and any audit text that should be appended.
func DiffAndRun(prev, doc *models.Document, actor string) []func(NotifyHook) {
	if doc == nil || len(doc.Rules) == 0 {
		return nil
	}
	events := diffEvents(prev, doc)
	if len(events) == 0 {
		return nil
	}
	pending := []func(NotifyHook){}
	for depth := 0; depth < MaxDepth && len(events) > 0; depth++ {
		nextEvents := []Event{}
		for _, evt := range events {
			for i := range doc.Rules {
				r := doc.Rules[i]
				if !r.Enabled || r.Trigger != evt.Type {
					continue
				}
				if !matchConditions(r.Conditions, evt) {
					continue
				}
				follow := applyActions(doc, r, evt, actor)
				nextEvents = append(nextEvents, follow...)
				for _, a := range r.Actions {
					if a.Type == models.ActionNotify {
						a := a // copy for closure
						r := r
						evt := evt
						pending = append(pending, func(hook NotifyHook) {
							if hook != nil {
								hook(r, a, evt)
							}
						})
					}
				}
			}
		}
		events = nextEvents
	}
	return pending
}

// diffEvents produces an event per relevant change between prev and
// doc. New tasks emit task.created; status / assignee changes on
// existing tasks emit the matching trigger; new comments emit
// comment.created.
func diffEvents(prev, doc *models.Document) []Event {
	prevTasks := map[int]models.Task{}
	if prev != nil {
		for _, t := range prev.Tasks {
			prevTasks[t.ID] = t
		}
	}
	out := []Event{}
	for i := range doc.Tasks {
		t := doc.Tasks[i]
		old, existed := prevTasks[t.ID]
		if !existed {
			out = append(out, Event{Type: models.TriggerTaskCreated, TaskID: t.ID, New: &doc.Tasks[i]})
			continue
		}
		if old.Status != t.Status {
			out = append(out, Event{
				Type: models.TriggerTaskStatusChanged, TaskID: t.ID,
				Old: copyTask(&old), New: &doc.Tasks[i],
			})
		}
		if old.Assignee != t.Assignee {
			out = append(out, Event{
				Type: models.TriggerTaskAssigneeChanged, TaskID: t.ID,
				Old: copyTask(&old), New: &doc.Tasks[i],
			})
		}
	}
	prevComments := map[int]struct{}{}
	if prev != nil {
		for _, c := range prev.TaskComments {
			prevComments[c.ID] = struct{}{}
		}
	}
	for i := range doc.TaskComments {
		c := doc.TaskComments[i]
		if _, existed := prevComments[c.ID]; existed {
			continue
		}
		// Surface the parent task on the event so conditions can
		// reference task.* fields when a comment fires.
		var parent *models.Task
		for ti := range doc.Tasks {
			if doc.Tasks[ti].ID == c.TaskID {
				parent = &doc.Tasks[ti]
				break
			}
		}
		out = append(out, Event{
			Type: models.TriggerCommentCreated, TaskID: c.TaskID,
			Comment: &doc.TaskComments[i], New: parent,
		})
	}
	prevEntityComments := map[int]struct{}{}
	if prev != nil {
		for _, c := range prev.EntityComments {
			prevEntityComments[c.ID] = struct{}{}
		}
	}
	for i := range doc.EntityComments {
		c := doc.EntityComments[i]
		if _, existed := prevEntityComments[c.ID]; existed {
			continue
		}
		out = append(out, Event{
			Type:          models.TriggerCommentCreated,
			EntityComment: &doc.EntityComments[i],
		})
	}
	return out
}

func copyTask(t *models.Task) *models.Task {
	c := *t
	return &c
}

// matchConditions evaluates the AND-joined condition list against the
// event's task. Unknown fields fail closed (rule does not fire).
func matchConditions(cs []models.RuleCondition, evt Event) bool {
	for _, c := range cs {
		got, ok := resolveField(c.Field, evt)
		if !ok {
			return false
		}
		if !applyOp(c.Op, got, c.Value) {
			return false
		}
	}
	return true
}

func resolveField(field string, evt Event) (string, bool) {
	field = strings.TrimSpace(field)
	if !strings.HasPrefix(field, "task.") {
		return "", false
	}
	if evt.New == nil {
		return "", false
	}
	t := evt.New
	switch field {
	case "task.status":
		return t.Status, true
	case "task.priority":
		return t.Priority, true
	case "task.project":
		return t.Project, true
	case "task.assignee":
		return t.Assignee, true
	case "task.type":
		if t.Type == "" {
			return models.TaskTypeTask, true
		}
		return t.Type, true
	case "task.done":
		if t.Done {
			return "true", true
		}
		return "false", true
	case "task.tags":
		return strings.Join(t.Tags, ","), true
	default:
		return "", false
	}
}

func applyOp(op, got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	switch op {
	case models.OpEq, "":
		return got == want
	case models.OpNe:
		return got != want
	case models.OpContains:
		return strings.Contains(strings.ToLower(got), strings.ToLower(want))
	case models.OpIn:
		for _, candidate := range strings.Split(want, ",") {
			if strings.TrimSpace(candidate) == got {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// applyActions mutates `doc` per the rule's action list and returns any
// follow-up events generated (used by the loop to re-evaluate rules
// against the post-action state).
func applyActions(doc *models.Document, rule models.Rule, evt Event, actor string) []Event {
	var follow []Event
	for _, a := range rule.Actions {
		switch a.Type {
		case models.ActionAssignTo:
			follow = append(follow, actionAssignTo(doc, a, evt, rule, actor)...)
		case models.ActionSetStatus:
			follow = append(follow, actionSetStatus(doc, a, evt, rule, actor)...)
		case models.ActionSetDueOffset:
			actionSetDueOffset(doc, a, evt, rule, actor)
		case models.ActionAddTag:
			actionAddTag(doc, a, evt, rule, actor)
		case models.ActionCreateSubtask:
			actionCreateSubtask(doc, a, evt, rule, actor)
		case models.ActionPostComment:
			actionPostComment(doc, a, evt, rule, actor)
		case models.ActionNotify:
			// Notify is handled post-persist; nothing to do here.
		}
	}
	return follow
}

func actionAssignTo(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) []Event {
	to := strings.TrimSpace(a.Params["to"])
	if to == "" || evt.New == nil {
		return nil
	}
	for i := range doc.Tasks {
		if doc.Tasks[i].ID != evt.TaskID {
			continue
		}
		if doc.Tasks[i].Assignee == to {
			return nil
		}
		old := doc.Tasks[i]
		doc.Tasks[i].Assignee = to
		tid := evt.TaskID
		models.AppendAudit(doc, "rule:"+rule.Name, "task.assigned",
			fmt.Sprintf("rule %q set assignee to %s on task #%d", rule.Name, to, tid), &tid)
		return []Event{{Type: models.TriggerTaskAssigneeChanged, TaskID: tid, Old: copyTask(&old), New: &doc.Tasks[i]}}
	}
	return nil
}

func actionSetStatus(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) []Event {
	status := strings.TrimSpace(a.Params["status"])
	if status == "" || evt.New == nil {
		return nil
	}
	for i := range doc.Tasks {
		if doc.Tasks[i].ID != evt.TaskID {
			continue
		}
		if doc.Tasks[i].Status == status {
			return nil
		}
		old := doc.Tasks[i]
		doc.Tasks[i].Status = status
		if status == "completed" {
			doc.Tasks[i].Done = true
		}
		tid := evt.TaskID
		models.AppendAudit(doc, "rule:"+rule.Name, "task.status_changed",
			fmt.Sprintf("rule %q set status to %s on task #%d", rule.Name, status, tid), &tid)
		return []Event{{Type: models.TriggerTaskStatusChanged, TaskID: tid, Old: copyTask(&old), New: &doc.Tasks[i]}}
	}
	return nil
}

func actionSetDueOffset(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) {
	daysStr := strings.TrimSpace(a.Params["days"])
	if daysStr == "" || evt.New == nil {
		return
	}
	days, ok := parseInt(daysStr)
	if !ok {
		return
	}
	for i := range doc.Tasks {
		if doc.Tasks[i].ID != evt.TaskID {
			continue
		}
		base := time.Now().UTC()
		// Prefer task.due as the anchor when present.
		if anchor, parsed := parseDateOrTime(doc.Tasks[i].Due); parsed {
			base = anchor
		}
		newDue := base.AddDate(0, 0, days).Format("2006-01-02")
		doc.Tasks[i].Due = newDue
		tid := evt.TaskID
		models.AppendAudit(doc, "rule:"+rule.Name, "task.due_set",
			fmt.Sprintf("rule %q set due to %s on task #%d", rule.Name, newDue, tid), &tid)
		return
	}
}

func actionAddTag(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) {
	tag := strings.TrimSpace(a.Params["tag"])
	if tag == "" || evt.New == nil {
		return
	}
	for i := range doc.Tasks {
		if doc.Tasks[i].ID != evt.TaskID {
			continue
		}
		for _, existing := range doc.Tasks[i].Tags {
			if existing == tag {
				return
			}
		}
		doc.Tasks[i].Tags = append(doc.Tasks[i].Tags, tag)
		tid := evt.TaskID
		models.AppendAudit(doc, "rule:"+rule.Name, "task.tag_added",
			fmt.Sprintf("rule %q added tag %q on task #%d", rule.Name, tag, tid), &tid)
		return
	}
}

func actionCreateSubtask(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) {
	name := strings.TrimSpace(a.Params["name"])
	if name == "" || evt.New == nil {
		return
	}
	order := 0
	for _, s := range doc.Subtasks {
		if s.ParentTaskID == evt.TaskID && s.Order >= order {
			order = s.Order + 1
		}
	}
	doc.Subtasks = append(doc.Subtasks, models.Subtask{
		ID:           models.NextSubtaskID(doc),
		ParentTaskID: evt.TaskID,
		Name:         name,
		Order:        order,
	})
	tid := evt.TaskID
	models.AppendAudit(doc, "rule:"+rule.Name, "subtask.created",
		fmt.Sprintf("rule %q added subtask %q to task #%d", rule.Name, name, tid), &tid)
}

func actionPostComment(doc *models.Document, a models.RuleAction, evt Event, rule models.Rule, actor string) {
	text := strings.TrimSpace(a.Params["text"])
	if text == "" {
		return
	}
	if evt.TaskID == 0 {
		return
	}
	doc.TaskComments = append(doc.TaskComments, models.TaskComment{
		ID:     models.NextCommentID(doc),
		TaskID: evt.TaskID,
		Author: "rule:" + rule.Name,
		Text:   text,
		Time:   models.ISONow(),
	})
}

// ----- helpers -----

func parseInt(s string) (int, bool) {
	n := 0
	negative := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		negative = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	if negative {
		n = -n
	}
	return n, true
}

func parseDateOrTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if v, err := time.Parse("2006-01-02", raw); err == nil {
		return v.UTC(), true
	}
	if v, err := time.Parse(time.RFC3339, raw); err == nil {
		return v.UTC(), true
	}
	return time.Time{}, false
}
