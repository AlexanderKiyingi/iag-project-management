package models

import "time"

func NextIntID(length int, getID func(int) int) int {
	max := 0
	for i := 0; i < length; i++ {
		if id := getID(i); id > max {
			max = id
		}
	}
	return max + 1
}

func NextTaskID(d *Document) int {
	return NextIntID(len(d.Tasks), func(i int) int { return d.Tasks[i].ID })
}

func NextGoalID(d *Document) int {
	return NextIntID(len(d.Goals), func(i int) int { return d.Goals[i].ID })
}

func NextSprintID(d *Document) int {
	return NextIntID(len(d.Sprints), func(i int) int { return d.Sprints[i].ID })
}

func NextChatID(d *Document) int {
	return NextIntID(len(d.Chats), func(i int) int { return d.Chats[i].ID })
}

func NextMessageID(d *Document) int {
	return NextIntID(len(d.Messages), func(i int) int { return d.Messages[i].ID })
}

func NextFileID(d *Document) int {
	return NextIntID(len(d.Files), func(i int) int { return d.Files[i].ID })
}

func NextCommentID(d *Document) int {
	return NextIntID(len(d.TaskComments), func(i int) int { return d.TaskComments[i].ID })
}

func NextSubtaskID(d *Document) int {
	return NextIntID(len(d.Subtasks), func(i int) int { return d.Subtasks[i].ID })
}

func NextRequisitionID(d *Document) int {
	return NextIntID(len(d.Requisitions), func(i int) int { return d.Requisitions[i].ID })
}

func NextAuditID(d *Document) int {
	return NextIntID(len(d.Audit), func(i int) int { return d.Audit[i].ID })
}

func AppendAudit(d *Document, actor, typ, text string, taskID *int) {
	d.Audit = append(d.Audit, AuditEntry{
		ID:     NextAuditID(d),
		Type:   typ,
		Actor:  actor,
		TaskID: taskID,
		Text:   text,
		Time:   ISONow(),
	})
}

func ISONow() string {
	return time.Now().UTC().Format(time.RFC3339)
}
