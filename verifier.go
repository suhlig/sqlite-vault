package sqlitevault

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Verifier checks that a backup can be downloaded, decrypted, and opened, and
// that its canary row is recent enough.
type Verifier struct {
	store       ObjectStore
	decryptor   func(inPath, outPath string) error
	canaryTable string
	logger      *slog.Logger
	nowFunc     func() time.Time
}

// NewVerifier constructs a Verifier using the given object store and age passphrase.
func NewVerifier(store ObjectStore, passphrase string) *Verifier {
	return &Verifier{
		store:       store,
		canaryTable: "backup_canary",
		decryptor: func(in, out string) error {
			return DecryptFile(in, out, passphrase)
		},
		logger:  slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		nowFunc: time.Now,
	}
}

// WithLogger sets the logger used by the verifier.
func (v *Verifier) WithLogger(l *slog.Logger) *Verifier {
	v.logger = l
	return v
}

// WithCanary sets the name of the canary table to read from the restored backup.
func (v *Verifier) WithCanary(tableName string) *Verifier {
	v.canaryTable = tableName
	return v
}

// WithDecryptor allows tests to inject a custom decryptor.
func (v *Verifier) WithDecryptor(f func(inPath, outPath string) error) *Verifier {
	v.decryptor = f
	return v
}

// WithNowFunc allows tests to control the reference time used for slot checks.
func (v *Verifier) WithNowFunc(f func() time.Time) *Verifier {
	v.nowFunc = f
	return v
}

// VerifyLatest resolves the latest alias for the given slot, downloads the
// backup it points to, and verifies it.
func (v *Verifier) VerifyLatest(ctx context.Context, prefix, slot string, maxAge time.Duration) error {
	aliasName := LatestAliasName(prefix, slot)

	tmpAlias, err := os.CreateTemp("", "vault-verify-*.alias")
	if err != nil {
		return fmt.Errorf("creating temp alias file: %w", err)
	}

	aliasPath := tmpAlias.Name()
	_ = tmpAlias.Close()
	defer func() {
		_ = os.Remove(aliasPath)
	}()

	v.logger.InfoContext(ctx, "Downloading alias", "alias", aliasName)
	if err := v.store.Retrieve(ctx, aliasName, aliasPath); err != nil {
		return fmt.Errorf("retrieving alias %q: %w", aliasName, err)
	}

	objectNameBytes, err := os.ReadFile(aliasPath)
	if err != nil {
		return fmt.Errorf("reading alias file: %w", err)
	}

	objectName := strings.TrimSpace(string(objectNameBytes))
	if objectName == "" {
		return fmt.Errorf("alias %q is empty", aliasName)
	}

	return v.verifyObject(ctx, objectName, slot, maxAge)
}

// Verify checks a specific backup object.
func (v *Verifier) Verify(ctx context.Context, objectName string, maxAge time.Duration) error {
	return v.verifyObject(ctx, objectName, "", maxAge)
}

func (v *Verifier) verifyObject(ctx context.Context, objectName, slot string, maxAge time.Duration) error {
	tmpAge, err := os.CreateTemp("", "vault-verify-*.db.age")
	if err != nil {
		return fmt.Errorf("creating temp age file: %w", err)
	}

	encPath := tmpAge.Name()
	_ = tmpAge.Close()
	defer func() {
		_ = os.Remove(encPath)
	}()

	v.logger.InfoContext(ctx, "Downloading backup for verification", "object", objectName)
	if err := v.store.Retrieve(ctx, objectName, encPath); err != nil {
		return fmt.Errorf("retrieving object %q: %w", objectName, err)
	}

	tmpDB := encPath + ".db"
	defer func() {
		_ = os.Remove(tmpDB)
	}()

	v.logger.InfoContext(ctx, "Decrypting backup")
	if err := v.decryptor(encPath, tmpDB); err != nil {
		return fmt.Errorf("decrypting backup: %w", err)
	}

	db, err := sql.Open("sqlite", tmpDB)
	if err != nil {
		return fmt.Errorf("opening restored database: %w", err)
	}

	defer func() {
		_ = db.Close()
	}()

	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("running integrity check: %w", err)
	}

	if integrity != "ok" {
		return fmt.Errorf("integrity check failed: %s", integrity)
	}

	var backedUpAt string
	if err := db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT backed_up_at FROM %s WHERE id = 1", v.canaryTable,
	)).Scan(&backedUpAt); err != nil {
		return fmt.Errorf("reading canary: %w", err)
	}

	t, err := time.Parse(time.RFC3339, backedUpAt)
	if err != nil {
		return fmt.Errorf("parsing canary timestamp: %w", err)
	}

	if slot != "" {
		if err := v.checkCanarySlot(t, slot); err != nil {
			return fmt.Errorf("canary outside expected backup window: %w", err)
		}
	}

	if v.nowFunc().Sub(t) > maxAge {
		return fmt.Errorf("backup canary too old: %s (max age %s)", t, maxAge)
	}

	v.logger.InfoContext(ctx, "Backup verification succeeded", "object", objectName, "backed_up_at", t)
	return nil
}

// checkCanarySlot verifies that t is within the expected window for the most
// recent backup of the given slot. It tolerates small scheduling delays.
func (v *Verifier) checkCanarySlot(t time.Time, slot string) error {
	expected, err := expectedBackupTime(slot, v.nowFunc().UTC())
	if err != nil {
		return err
	}

	const tolerance = 5 * time.Minute

	if t.Before(expected.Add(-tolerance)) {
		return fmt.Errorf("canary timestamp %s is before expected slot time %s (slot %q)", t, expected, slot)
	}

	return nil
}

// expectedBackupTime returns the scheduled time of the most recent backup for
// the given slot. It assumes the same schedule as ObjectName:
//   - hourly: every hour except 04:00
//   - daily: 04:00 on non-Sunday days
//   - weekly: 04:00 on Sundays that are not the last Sunday of the year
//   - yearly: 04:00 on the last Sunday of the year
func expectedBackupTime(slot string, now time.Time) (time.Time, error) {
	now = now.UTC()
	loc := time.UTC

	switch slot {
	case "hourly":
		// Most recent hour start, except 04:00 which is handled by daily/weekly/yearly.
		t := now.Truncate(time.Hour)
		if t.Hour() == 4 {
			t = t.Add(-time.Hour)
		}
		return t, nil

	case "daily":
		t := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, loc)
		if now.Before(t) {
			t = t.AddDate(0, 0, -1)
		}
		// Skip Sundays that are not the last Sunday of the year (weekly) and the last Sunday of the year (yearly).
		for t.Weekday() == time.Sunday || lastSundayOfTheYear(t) {
			t = t.AddDate(0, 0, -1)
		}
		return t, nil

	case "weekly":
		t := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, loc)
		offset := (int(t.Weekday()) - int(time.Sunday) + 7) % 7
		t = t.AddDate(0, 0, -offset)
		if now.Before(t) {
			t = t.AddDate(0, 0, -7)
		}
		for lastSundayOfTheYear(t) {
			t = t.AddDate(0, 0, -7)
		}
		return t, nil

	case "yearly":
		for year := now.Year(); year >= 0; year-- {
			backup := yearlyBackupTime(year, loc)
			if !backup.After(now) {
				return backup, nil
			}
		}
		return time.Time{}, fmt.Errorf("no yearly backup expected before %s", now)

	default:
		return time.Time{}, fmt.Errorf("unknown slot %q", slot)
	}
}

func yearlyBackupTime(year int, loc *time.Location) time.Time {
	dec31 := time.Date(year, time.December, 31, 0, 0, 0, 0, loc)
	offset := (int(dec31.Weekday()) - int(time.Sunday) + 7) % 7
	lastSun := dec31.AddDate(0, 0, -offset)
	return time.Date(lastSun.Year(), lastSun.Month(), lastSun.Day(), 4, 0, 0, 0, loc)
}
