-- Which backend holds a blob's bytes. Rows written before object storage
-- existed are on local disk, so the default is 'disk'; new uploads record 's3'
-- when S3_* is configured. Reads route on this column, so a disk row is never
-- looked up in the bucket (or the reverse). cmd/backfill-files copies disk rows
-- into the bucket and flips them to 's3'.
ALTER TABLE pm_file_blobs
    ADD COLUMN IF NOT EXISTS storage_backend TEXT NOT NULL DEFAULT 'disk';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'pm_file_blobs'::regclass
          AND conname = 'pm_file_blobs_storage_backend_check'
    ) THEN
        ALTER TABLE pm_file_blobs
            ADD CONSTRAINT pm_file_blobs_storage_backend_check
            CHECK (storage_backend IN ('disk', 's3'));
    END IF;
END $$;
