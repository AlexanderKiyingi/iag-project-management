package store

import (
	"context"
)

func (r *Repository) WasReminderSent(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pm_reminder_sent WHERE reminder_key = $1)`,
		key,
	).Scan(&exists)
	return exists, err
}

func (r *Repository) MarkReminderSent(ctx context.Context, key string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO pm_reminder_sent (reminder_key) VALUES ($1) ON CONFLICT (reminder_key) DO NOTHING`,
		key,
	)
	return err
}
