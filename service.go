package sqlitevault

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

// Service orchestrates creating SQLite backups, encrypting them, and storing them in object storage.
type Service struct {
	objectStore  ObjectStore
	dbURL        string
	objectPrefix string
	encryptor    func(inPath, outPath string) error
	logger       *slog.Logger
	canaryTable  string
}

// NewService constructs a Service for the given SQLite database URL.
// The returned Service defaults to using the object prefix "backup".
func NewService(dbURL string, o ObjectStore) *Service {
	return &Service{
		objectStore:  o,
		objectPrefix: "backup",
		dbURL:        dbURL,
		logger:       slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

// WithObjectPrefix sets the object name prefix used when storing backups and returns the Service.
func (s *Service) WithObjectPrefix(p string) *Service {
	s.objectPrefix = p
	return s
}

func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

// WithEncryptor allows tests to inject a custom encryptor. In production, prefer WithPassphrase.
func (s *Service) WithEncryptor(f func(inPath, outPath string) error) *Service {
	s.encryptor = f
	return s
}

// WithCanary enables writing a canary row to the given table before each backup.
// The table name must be a valid SQLite identifier. An error is returned immediately
// for invalid names so that configuration problems surface at startup.
func (s *Service) WithCanary(tableName string) (*Service, error) {
	if !safeIdentifier(tableName) {
		return s, fmt.Errorf("invalid canary table name %q", tableName)
	}

	s.canaryTable = tableName
	return s, nil
}

var identifierRegexp = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func safeIdentifier(name string) bool {
	if !identifierRegexp.MatchString(name) {
		return false
	}

	// SQLite reserves names that begin with "sqlite_".
	return !strings.HasPrefix(strings.ToLower(name), "sqlite_")
}

func (s *Service) backupOnce(ctx context.Context, now time.Time) (string, string, error) {
	if s.encryptor == nil {
		return "", "", fmt.Errorf("encryption is not configured; refusing to upload plaintext backup")
	}

	// runCtx is a per-run context with deadline. It prevents long blocking during backup.
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	db, err := sql.Open("sqlite", s.dbURL)

	if err != nil {
		return "", "", fmt.Errorf("opening database: %w", err)
	}

	defer func() {
		_ = db.Close()
	}()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	conn, err := db.Conn(runCtx)

	if err != nil {
		return "", "", fmt.Errorf("getting dedicated connection: %w", err)
	}

	defer func() {
		_ = conn.Close()
	}()

	// Wait briefly if the DB is busy, instead of immediately failing.
	if _, err = conn.ExecContext(runCtx, "PRAGMA busy_timeout=3000"); err != nil {
		return "", "", fmt.Errorf("setting busy_timeout: %w", err)
	}

	// Ensure WAL mode; harmless if already enabled.
	if _, err = conn.ExecContext(runCtx, "PRAGMA journal_mode=WAL"); err != nil {
		return "", "", fmt.Errorf("enabling WAL: %w", err)
	}

	tempFile, err := os.CreateTemp("", fmt.Sprintf("%s-*.db", s.objectPrefix))

	if err != nil {
		return "", "", fmt.Errorf("creating temp file: %w", err)
	}

	// Best-effort close; non-fatal if it fails.
	_ = tempFile.Close()

	localPath := tempFile.Name()

	if s.canaryTable != "" {
		if err := s.writeCanary(runCtx, conn, now); err != nil {
			_ = os.Remove(localPath)
			return "", "", fmt.Errorf("writing canary: %w", err)
		}
	}

	_, err = conn.ExecContext(runCtx, fmt.Sprintf("VACUUM INTO '%s'", localPath))

	if err != nil {
		_ = os.Remove(localPath)
		return "", "", fmt.Errorf("performing VACUUM INTO: %w", err)
	}

	encPath := localPath + ".age"

	// Encrypt the backup with age (scrypt) using the configured passphrase recipients.
	err = s.encryptor(localPath, encPath)

	if err != nil {
		_ = os.Remove(localPath)
		return "", "", fmt.Errorf("encryption failed: %w", err)
	}

	// Remove plaintext after successful encryption (best-effort).
	_ = os.Remove(localPath)

	// Ensure encrypted temp file is removed at the end (best-effort).
	defer func() {
		_ = os.Remove(encPath)
	}()

	objectName := ObjectName(s.objectPrefix, now, ".db.age")
	sha1sum, err := s.objectStore.Store(runCtx, encPath, objectName)

	if err != nil {
		return "", "", fmt.Errorf("backup upload failed (%s): %w", Slot(now), err)
	}

	if err := s.writeAlias(runCtx, objectName, now); err != nil {
		return "", "", fmt.Errorf("alias upload failed (%s): %w", Slot(now), err)
	}

	return objectName, sha1sum, nil
}

func (s *Service) writeCanary(ctx context.Context, conn *sql.Conn, now time.Time) error {
	jobID := make([]byte, 16)
	if _, err := rand.Read(jobID); err != nil {
		return fmt.Errorf("generating canary job id: %w", err)
	}

	_, err := conn.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			job_id TEXT NOT NULL,
			backed_up_at TEXT NOT NULL
		)
	`, s.canaryTable))
	if err != nil {
		return fmt.Errorf("creating canary table: %w", err)
	}

	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, job_id, backed_up_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			job_id = excluded.job_id,
			backed_up_at = excluded.backed_up_at
	`, s.canaryTable),
		hex.EncodeToString(jobID),
		now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("writing canary row: %w", err)
	}

	return nil
}

func (s *Service) writeAlias(ctx context.Context, objectName string, now time.Time) error {
	aliasPath, err := os.CreateTemp("", fmt.Sprintf("%s-*.alias", s.objectPrefix))
	if err != nil {
		return fmt.Errorf("creating alias temp file: %w", err)
	}

	defer func() {
		_ = os.Remove(aliasPath.Name())
	}()

	if _, err := aliasPath.WriteString(objectName); err != nil {
		_ = aliasPath.Close()
		return fmt.Errorf("writing alias content: %w", err)
	}

	if err := aliasPath.Close(); err != nil {
		return fmt.Errorf("closing alias temp file: %w", err)
	}

	slot := Slot(now)
	if _, err := s.objectStore.Store(ctx, aliasPath.Name(), LatestAliasName(s.objectPrefix, slot)); err != nil {
		return fmt.Errorf("storing alias for slot %q: %w", slot, err)
	}

	return nil
}

// BackupFunc performs a single backup run at the provided time.
// It is intended to be scheduled by Scheduler and logs success or failure.
func (s *Service) BackupFunc(ctx context.Context, now time.Time) {
	s.logger.InfoContext(ctx, "Performing backup")

	objectName, digest, err := s.backupOnce(ctx, now)

	if err != nil {
		s.logger.ErrorContext(ctx, "Backup failed", "error", err)
		return
	}

	s.logger.InfoContext(ctx, "Backup succeeded", "objectName", objectName, "digest", digest, "canary_table", s.canaryTable)
}
