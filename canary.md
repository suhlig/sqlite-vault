# Backup Canary & Verification Plan

This document describes how to add an end-to-end backup verification canary to sqlite-vault. The goal is to detect backups that are empty, corrupt, cannot be decrypted, or simply stale, by having a second, external process regularly restore the latest backup and check a well-known row inside it.

## Design decisions

### Backwards compatibility is not required

Because this is an internal project and we do not need to preserve existing APIs, we will keep the surface small: extend `ObjectStore` directly with a `Retrieve` method. `MinioStore` will implement both upload and download.

There is no separate `ObjectRetriever` interface.

### The library creates the canary table

The application only tells the library the name of the canary table via `WithCanary("sqlite_vault_canary")`. The library then creates the table if it does not exist and writes the single row before `VACUUM INTO`.

If an application wants full control over the schema, it can create the table beforehand; the library uses `CREATE TABLE IF NOT EXISTS`, so it will not overwrite an existing table.

### Latest aliases are implemented

The README already documents `*-latest.alias` alias files, but the code does not write them. We will implement them as part of this work so the verifier has a stable object name to download. The alias content is the object name of the actual backup it points to.

### Verification is a separate process

The verifier is a separate type and a separate CLI binary. It should run in a different environment (container, account, or host) from the backup process and should not have write access to the source database.

### Configuration errors fail fast; runtime errors are logged

Constructor-style methods such as `NewService`, `WithPassphrase`, and `WithCanary` return errors immediately. The calling application should treat these as fatal: if the backup configuration is wrong, the application should not start and pretend everything is fine.

Runtime failures inside the scheduled backup (S3 unreachable, DB temporarily locked, network timeout) are caught by `BackupFunc`, logged, and not propagated. The scheduler continues to run the next backup slot. This keeps a transient infrastructure problem from crashing the main application.

Because runtime errors are non-fatal, they must be observable through logs, metrics, or an external health check. The verifier itself also acts as an end-to-end health check: if backups silently stop working, the verifier will eventually fail.

## Files to change

| File | Change |
|------|--------|
| `store.go` | Extend `ObjectStore` with `Retrieve`. Implement `MinioStore.Retrieve`. |
| `age_enc.go` | Add `DecryptFile(inPath, outPath, passphrase string) error`. |
| `service.go` | Add `canaryTable` field, `WithCanary`, canary write logic, and latest-alias write logic. |
| `naming.go` | Add helpers for latest alias names and slot classification. |
| `verifier.go` | New file: `Verifier` type and `Verify` method. |
| `cmd/sqlite-vault-verify/main.go` | New CLI binary. |
| `e2e_test.go` | Extend to cover canary + verification. |
| `service_test.go` | Add canary-specific tests. |
| `README.markdown` | Update to match the actual implementation. |
| `canary.md` | This file. |

## Public API

### `ObjectStore`

```go
type ObjectStore interface {
    Store(ctx context.Context, localPath, objectName string) (string, error)
    Retrieve(ctx context.Context, objectName, localPath string) error
}
```

`MinioStore.Retrieve` uses `minio.Client.FGetObject`.

### `Service`

```go
func NewService(dbURL string, o ObjectStore) *Service
func (s *Service) WithObjectPrefix(p string) *Service
func (s *Service) WithPassphrase(pw string) (*Service, error)
func (s *Service) WithLogger(l *slog.Logger) *Service
func (s *Service) WithCanary(tableName string) (*Service, error)   // NEW
func (s *Service) WithEncryptor(f func(inPath, outPath string) error) *Service
```

`WithCanary` validates the table name immediately and returns an error if it is not a valid SQLite identifier. The table is created and updated only when `backupOnce` runs. Returning the error early means the application knows the backup is going to fail before the first backup run.

### `Verifier`

```go
func NewVerifier(store ObjectStore, passphrase string) *Verifier
func (v *Verifier) WithCanary(tableName string) *Verifier
func (v *Verifier) WithLogger(l *slog.Logger) *Verifier
func (v *Verifier) WithDecryptor(f func(inPath, outPath string) error) *Verifier
func (v *Verifier) Verify(ctx context.Context, objectName string, maxAge time.Duration) error
```

`Verify` downloads `objectName`, decrypts it, opens the SQLite file read-only, runs `PRAGMA integrity_check`, and checks the canary timestamp.

## Canary table schema

```sql
CREATE TABLE IF NOT EXISTS <name> (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    job_id TEXT NOT NULL,
    backed_up_at TEXT NOT NULL
);
```

The library updates the single row before each backup:

```sql
INSERT INTO <name> (id, job_id, backed_up_at)
VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    job_id = excluded.job_id,
    backed_up_at = excluded.backed_up_at;
```

- `job_id` is a random 16-byte value, hex-encoded. It is recorded for forensics and may be used for stronger verification later, but the first implementation does not check it.
- `backed_up_at` is the backup time in RFC3339 UTC.

The table name is validated with a simple regex to prevent SQL injection, something like `^[a-zA-Z_][a-zA-Z0-9_]*$`.

## Backup flow

1. Open the source database.
2. If `WithCanary` was configured:
   - Create the canary table if it does not exist.
   - Write the fresh `job_id` + `backed_up_at` row.
3. Run `VACUUM INTO` to a temp file.
4. Encrypt the temp file with age.
5. Upload the encrypted file to the regular backup object name.
6. If the upload succeeds, write the appropriate `*-latest.alias` file(s) for the current slot (hourly, daily, weekly, yearly).

The canary row is written before `VACUUM INTO` so it is guaranteed to be part of the snapshot.

## Latest alias files

An alias is a small plain-text object whose content is the object name of the backup it points to.

Naming:

| Slot | Alias object name |
|------|-------------------|
| Hourly | `<prefix>.hourly-latest.alias` |
| Daily  | `<prefix>.daily-latest.alias` |
| Weekly | `<prefix>.weekly-latest.alias` |
| Yearly | `<prefix>.yearly-latest.alias` |

The alias is updated only after the corresponding backup upload succeeds.

The alias uses the `.alias` extension because it is plain text and not encrypted, unlike the actual backup files.

## Verification flow

1. Resolve the alias object name from the CLI `-alias` flag (default `daily-latest.alias`) and the configured prefix.
2. Download the alias file and read the actual backup object name.
3. Download the actual backup object to a temp file.
4. Decrypt the backup with age.
5. Open the decrypted SQLite file read-only.
6. Run `PRAGMA integrity_check` and require the result to be exactly `ok`.
7. Read `backed_up_at` from the canary table.
8. Parse the timestamp and verify that it corresponds to the most recent scheduled backup run for the selected alias. For example, a `daily-latest.alias` verified at 6 AM should point to today's 4 AM backup; if verified at 3 AM, it may legitimately point to yesterday's 4 AM backup. The verifier derives the expected window from the slot (hourly, daily, weekly, yearly) and the current time. The daily, weekly, and yearly slots are all scheduled at 4 AM.
9. As a coarse safety net, also verify that `time.Since(t)` does not exceed the configured `maxAge`.
10. Log the result and return `nil` or an error.

On failure, the error should be descriptive enough to tell which step failed (download, decrypt, integrity check, stale canary, or canary outside the expected backup window).

## CLI: `cmd/sqlite-vault-verify`

Sensitive values are read only from files or Docker secrets. All other configuration is supplied via command-line flags.

Command-line arguments are visible in `ps` and `/proc/<pid>/cmdline`, but they are fine for non-sensitive values. Files mounted into the container are the simplest safe default for secrets.

### Non-sensitive flags

| Flag | Description |
|------|-------------|
| `-endpoint` | S3 endpoint (e.g. `s3.amazonaws.com` or `minio.example.com`) |
| `-bucket` | Bucket name |
| `-region` | S3 region |
| `-prefix` | Object prefix, e.g. `myapp` |
| `-canary-table` | Canary table name (default: `sqlite_vault_canary`) |
| `-max-age` | Maximum acceptable canary age, e.g. `26h` |
| `-alias` | Which latest alias to verify (default: `daily-latest.alias`) |
| `-timeout` | Overall timeout for the verification run |
| `-insecure` | Skip TLS verification (useful for local MinIO) |

### Secret flags

Secrets are read from files. The flag gives the path to the file containing the secret.

| Secret | Flag |
|--------|------|
| S3 access key | `-access-key-file` |
| S3 secret key | `-secret-key-file` |
| age passphrase | `-passphrase-file` |

The file must contain the secret value only, with no trailing newline required.

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Verification succeeded |
| `1` | Verification failed |

### Example: command line

```bash
sqlite-vault-verify \
  -endpoint s3.amazonaws.com \
  -bucket my-backups \
  -region us-east-1 \
  -prefix myapp \
  -max-age 26h \
  -access-key-file /run/secrets/access_key \
  -secret-key-file /run/secrets/secret_key \
  -passphrase-file /run/secrets/passphrase
```

### Example: Docker with mounted secret files

Create the secret files on the host:

```bash
printf '%s' 'AKIA...' > /host/secrets/access_key
printf '%s' '...'     > /host/secrets/secret_key
printf '%s' '...'     > /host/secrets/passphrase
```

Run the verifier with the non-sensitive values as flags and the secrets as read-only mounts:

```bash
docker run --rm \
  -v /host/secrets/access_key:/run/secrets/access_key:ro \
  -v /host/secrets/secret_key:/run/secrets/secret_key:ro \
  -v /host/secrets/passphrase:/run/secrets/passphrase:ro \
  suhlig/sqlite-vault-verify \
  -endpoint s3.amazonaws.com \
  -bucket my-backups \
  -region us-east-1 \
  -prefix myapp \
  -max-age 26h \
  -alias daily-latest.alias \
  -access-key-file /run/secrets/access_key \
  -secret-key-file /run/secrets/secret_key \
  -passphrase-file /run/secrets/passphrase
```

> Note that bind mounts must be placed before the image name in the `docker run` argument list.

The CLI is intended to be run from a cron job, systemd timer, Kubernetes CronJob, or Docker scheduler that is separate from the backup process.

## Testing plan

### Unit tests

- `DecryptFile` round-trip with `encryptFile`.
- `safeIdentifier` accepts valid identifiers and rejects invalid ones.
- `LatestAliasName` and slot classification helpers.

### Service tests

- `WithCanary` causes the canary table to be created and the row to be present in the uploaded backup.
- The backup job ID changes between runs.
- Canary creation is skipped when `WithCanary` is not called.

### E2E tests

Extend the existing e2e test to:

1. Configure the service with `WithCanary`.
2. Run a backup.
3. Use a `memoryStore` that also supports `Retrieve`.
4. Run `NewVerifier(...).Verify` on the latest backup object.
5. Assert that verification succeeds.

Negative e2e tests:

- Corrupt the encrypted bytes before verification and expect a decryption failure.
- Drop the canary table after restore and expect a "canary missing" error.
- Write an old `backed_up_at` value and expect a "stale canary" error.
- Corrupt the restored SQLite file and expect an integrity check failure.

## Future work (not in this implementation)

- Spot-check older backups: once a week or month, pick a random historical backup object and verify it. This catches retention issues that only checking `latest` would miss.
- Store the expected backup digest in a separate, tamper-resistant location and compare it during verification.
- Add cryptographic signatures to backups. age encrypts but does not sign; a separate signature (e.g. minisign) would protect against malicious object replacement.
- Support verifying without aliases by deriving the object name from a given time.
