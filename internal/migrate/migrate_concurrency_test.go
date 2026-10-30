package migrate

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pmdb "github.com/iag/project-management/backend/db"
	"github.com/iag/project-management/backend/internal/db"
)

// TestUpConcurrentReplicas boots five replicas at once against one empty
// database. Every Up must succeed and each migration must be recorded exactly
// once.
//
// PM_TEST_DSN must be allowed to CREATE DATABASE: the test migrates a database of
// its own and drops it afterwards, so it starts empty whatever else has run.
func TestUpConcurrentReplicas(t *testing.T) {
	dsn := os.Getenv("PM_TEST_DSN")
	if dsn == "" {
		t.Skip("PM_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	name := fmt.Sprintf("pm_migrate_concurrency_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name

	const replicas = 5
	pools := make([]*pgxpool.Pool, replicas)
	for i := range pools {
		p, err := db.Connect(ctx, u.String())
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer p.Close()
		pools[i] = p
	}

	var wg sync.WaitGroup
	errs := make([]error, replicas)
	start := make(chan struct{})
	for i := range pools {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = Up(ctx, pools[i], pmdb.Migrations())
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d: %v", i, err)
		}
	}

	migs, err := load(pmdb.Migrations())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var got int
	if err := pools[0].QueryRow(ctx, `SELECT count(*) FROM iag_pm.schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if got != len(migs) {
		t.Fatalf("ledger holds %d migrations, want %d", got, len(migs))
	}
}
