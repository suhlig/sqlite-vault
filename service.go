package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Service orchestrates creating SQLite backups, encrypting them, and storing them in object storage.
type Service struct {
	objectStore  ObjectStore
	dbURL        string
	objectPrefix string
	encryptor    func(inPath, outPath string) error
	logger       *slog.Logger
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

func (s *Service) backupOnce(ctx context.Context, now time.Time) (string, error) {
	if s.encryptor == nil {
		return "", fmt.Errorf("encryption is not configured; refusing to upload plaintext backup")
	}

	// runCtx is a per-run context with deadline. It prevents long blocking during backup.
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	db, err := sql.Open("sqlite", s.dbURL)

	if err != nil {
		return "", fmt.Errorf("opening database: %w", err)
	}

	defer func() {
		_ = db.Close()
	}()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	conn, err := db.Conn(runCtx)

	if err != nil {
		return "", fmt.Errorf("getting dedicated connection: %w", err)
	}

	defer func() {
		_ = conn.Close()
	}()

	// Wait briefly if the DB is busy, instead of immediately failing.
	if _, err = conn.ExecContext(runCtx, "PRAGMA busy_timeout=3000"); err != nil {
		return "", fmt.Errorf("setting busy_timeout: %w", err)
	}

	// Ensure WAL mode; harmless if already enabled.
	if _, err = conn.ExecContext(runCtx, "PRAGMA journal_mode=WAL"); err != nil {
		return "", fmt.Errorf("enabling WAL: %w", err)
	}

	tempFile, err := os.CreateTemp("", fmt.Sprintf("%s-*.db", s.objectPrefix))

	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}

	// Best-effort close; non-fatal if it fails.
	_ = tempFile.Close()

	localPath := tempFile.Name()

	_, err = conn.ExecContext(runCtx, fmt.Sprintf("VACUUM INTO '%s'", localPath))

	if err != nil {
		_ = os.Remove(localPath)
		return "", fmt.Errorf("performing VACUUM INTO: %w", err)
	}

	encPath := localPath + ".age"

	// Encrypt the backup with age (scrypt) using the configured passphrase recipients.
	err = s.encryptor(localPath, encPath)

	if err != nil {
		_ = os.Remove(localPath)
		return "", fmt.Errorf("encryption failed: %w", err)
	}

	// Remove plaintext after successful encryption (best-effort).
	_ = os.Remove(localPath)

	// Ensure encrypted temp file is removed at the end (best-effort).
	defer func() {
		_ = os.Remove(encPath)
	}()

	sha1sum, err := s.objectStore.Store(runCtx, encPath, ObjectName(s.objectPrefix, now, ".db.age"))

	if err != nil {
		return "", fmt.Errorf("backup upload failed (hourly): %w", err)
	}

	return sha1sum, nil
}

// BackupFunc performs a single backup run at the provided time.
// It is intended to be scheduled by Scheduler and logs success or failure.
func (s *Service) BackupFunc(ctx context.Context, now time.Time) {
	s.logger.InfoContext(ctx, "Performing backup")

	digest, err := s.backupOnce(ctx, now)

	if err != nil {
		s.logger.ErrorContext(ctx, "Backup failed", "error", err)
		return
	}

	s.logger.InfoContext(ctx, "Backup succeeded", "digest", digest)
}
