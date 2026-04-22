# SQLite Vault

Encrypted SQLite backup to S3-compatible storage with scheduled retention.

# Overview

SQLite Vault provides a simple, secure way to backup SQLite databases with:

- **Encryption**: Uses [age](https://github.com/FiloSottile/age) with scrypt for symmetric encryption
- **S3-Compatible Storage**: Stores encrypted backups in any S3-compatible object store (AWS S3, MinIO, Backblaze B2, etc.)
- **Smart Retention**: Automatically creates hourly, daily, weekly, and yearly backups with intelligent naming
- **Scheduled Backups**: Built-in scheduler for running backups at configurable intervals

# Features

- Backup SQLite databases using `VACUUM INTO` for consistent snapshots
- Encrypt backups with age (scrypt passphrase-based encryption)
- Upload to S3-compatible storage via MinIO client
- Automatic cleanup of temporary files
- Context-aware operations with timeouts
- Structured logging via `log/slog`

# Installation

```bash
go get github.com/suhlig/sqlite-vault
```

# Quick Start

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/suhlig/sqlite-vault"
)

func main() {
	minioClient, err := minio.New("s3.amazonaws.com", &minio.Options{
		Creds:  credentials.NewStaticV4("ACCESS_KEY", "SECRET_KEY", ""),
		Region: "us-east-1",
		Secure: true,
	})

if err != nil {
		log.Fatal(err)
	}

	store, err := backup.NewMinioStore(minioClient, "my-backups")

	if err != nil {
		log.Fatal(err)
	}

	svc := backup.NewService("file:mydb.sqlite", store).
		WithObjectPrefix("myapp").
		WithObjectPrefix("backups")


  svc, err = svc.WithPassphrase("my-secret-passphrase")

  if err != nil {
		log.Fatal(err)
	}

	scheduler := backup.NewScheduler(svc.BackupFunc)

  if err := scheduler.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	select {} // block exiting unless something else already is
}
```

# Backup Naming Convention

Backups are automatically named based on when they are created:

| Time | Example Name |
|------|--------------|
| Hourly (any hour except 4 AM) | `myapp.hourly-09.db.age` |
| Daily (4 AM, Mon-Sat) | `myapp.daily-Monday.db.age` |
| Weekly (4 AM on Sunday) | `myapp.weekly-10.db.age` |
| Yearly (4 AM on last Sunday of year) | `myapp.yearly-2024.db.age` |

# Retention

The backup system uses a naming scheme that automatically manages retention by overwriting the last backup in the cycle:

- **Daily Backups**: Files are named with the day of the week (e.g., `Monday.db`, `Tuesday.db`, etc.). This ensures that there's always a backup for each of the last 7 days. When a new backup is created on a given day, it overwrites the backup from the previous week.

- **Weekly Backups**: Files are named with the ISO week number (e.g., `week-01.db`, `week-02.db`, ..., `week-53.db`). This maintains a backup for each of the last 53 weeks. When a new year begins and week numbers restart, the previous year's weekly backup for that week number is overwritten.

This naming strategy provides a simple and effective retention policy without requiring explicit deletion of old backups. Recent data has daily granularity, while older data is preserved with weekly granularity for up to a year.

Make sure the S3 bucket has versioning disabled; otherwise backups would not be replaced, but kept forever.

# Configuration

## S3 Bucket

Here are example commands for Backblaze B2:

1. Create the bucket

    ```command
    b2 bucket create sqlite-vault-demo-backup allPrivate --lifecycle-rule '{"daysFromHidingToDeleting":1,"daysFromUploadingToHiding":null,"fileNamePrefix":""}'
    ```

1. Create the application key:

    ```command
    b2 key create --bucket sqlite-vault-demo-backup sqlite-vault-demo-backup-writer listFiles,readFiles,writeFiles
    ```

    Be sure to save the output; it's not going to be shown again.

# Restore

1. Download the `.age` file from S3, e.g. using

    ```command
    $ b2 file download --no-progress b2://sqlite-vault-demo-backup/myapp.hourly-09.db.age myapp.db.age
    ```

1. Decrypt with

    ```command
    rage --decrypt --output myapp.db myapp.db.age
    ```

    You may use any other age-compatible implementation instead of rage.

1. Check if your data is accessible with

    ```command
    sqlite3 myapp.db
    ```

## Dependencies

- [age](https://github.com/FiloSottile/age) - Modern encryption tool
- [minio-go](https://github.com/minio/minio-go) - S3-compatible client
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) - Pure Go SQLite driver

## License

MIT
