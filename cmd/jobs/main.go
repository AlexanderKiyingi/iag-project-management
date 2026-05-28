// Command jobs runs scheduled project-management maintenance.
//
// Examples:
//
//	DATABASE_URL=... EVENT_BUS_ENABLED=true KAFKA_BROKERS=localhost:19092 \
//	  NOTIFY_DEFAULT_RECIPIENT=ops@iag.local go run ./cmd/jobs --reminders
//
//	DATABASE_URL=... go run ./cmd/jobs --archive
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/iag/project-management/backend/internal/db"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/jobs"
	"github.com/iag/project-management/backend/internal/store"
)

func main() {
	reminders := flag.Bool("reminders", false, "emit pm.alert.raised for message reminders and tasks due within TASK_DUE_REMINDER_DAYS")
	archive := flag.Bool("archive", false, "trim audit, notifications, and messages from inactive chats per retention env vars")
	flag.Parse()

	if !*reminders && !*archive {
		fmt.Fprintln(os.Stderr, "specify --reminders or --archive")
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, "")
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}
	defer pool.Close()

	repo := store.NewRepository(pool)
	bus := events.NewFromEnv()
	defer func() { _ = bus.Close() }()

	if *reminders {
		if _, err := jobs.RunReminders(ctx, repo, bus); err != nil {
			log.Fatalf("reminders: %v", err)
		}
	}
	if *archive {
		if _, err := jobs.RunArchive(ctx, repo, jobs.DefaultArchiveConfig()); err != nil {
			log.Fatalf("archive: %v", err)
		}
	}
}
