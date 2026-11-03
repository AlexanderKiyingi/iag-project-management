// Command backfill-files copies project-management blobs written to local disk
// (PM_UPLOAD_DIR) into the S3-compatible bucket and marks them s3, so they stay
// readable once the service runs from object storage without a volume.
//
// It must run where the disk files are: on the Railway service, with the volume
// still mounted and S3_* set. It inherits DATABASE_URL, PM_UPLOAD_DIR and S3_*
// from the service. Idempotent; rows already on s3 are skipped, and rows whose
// file is missing from disk are reported and left alone.
//
//	/app/backfill-files -dry-run   report what would move, change nothing
//	/app/backfill-files            upload and flip
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/iag/project-management/backend/internal/config"
	"github.com/iag/project-management/backend/internal/db"
	"github.com/iag/project-management/backend/internal/files"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would be uploaded and change nothing")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, "")
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}
	defer pool.Close()

	s3 := files.NewS3(cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
		cfg.S3AccessKeyID, cfg.S3SecretAccessKey, cfg.S3UseSSL)
	store := files.NewStore(pool, cfg.UploadDir, s3)
	res, err := store.Backfill(ctx, *dryRun, log.Printf)
	if err != nil {
		log.Fatalf("backfill: %v (so far: %+v)", err, res)
	}
	log.Printf("backfill done: %d disk rows, %d moved, %d missing on disk (dir %s, dry-run %v)",
		res.Pending, res.Moved, res.Missing, cfg.UploadDir, *dryRun)
}
