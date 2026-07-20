package sqlitevault_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sqlitevault "github.com/suhlig/sqlite-vault"

	_ "modernc.org/sqlite"
)

type fakeVerifierStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeVerifierStore() *fakeVerifierStore {
	return &fakeVerifierStore{objects: make(map[string][]byte)}
}

func (f *fakeVerifierStore) Store(_ context.Context, localPath, objectName string) (string, error) {
	b, err := os.ReadFile(localPath)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	f.objects[objectName] = b
	f.mu.Unlock()

	return "digest", nil
}

func (f *fakeVerifierStore) Retrieve(_ context.Context, objectName, localPath string) error {
	f.mu.Lock()
	b, ok := f.objects[objectName]
	f.mu.Unlock()

	if !ok {
		return fmt.Errorf("object %q not found", objectName)
	}

	return os.WriteFile(localPath, b, 0600)
}

func (f *fakeVerifierStore) Put(name string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[name] = b
}

var _ = Describe("Verifier", func() {
	var (
		store    *fakeVerifierStore
		dbPath   string
		now      time.Time
		verifier *sqlitevault.Verifier
	)

	BeforeEach(func() {
		store = newFakeVerifierStore()
		now = time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)

		// Build a valid, decrypted SQLite database that already contains a canary row.
		tmpDir, err := os.MkdirTemp("", "verifier-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmpDir)

		dbPath = filepath.Join(tmpDir, "canary.db")
		db, err := sql.Open("sqlite", dbPath)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		_, err = db.Exec(`
			CREATE TABLE backup_canary (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				job_id TEXT NOT NULL,
				backed_up_at TEXT NOT NULL
			)
		`)
		Expect(err).NotTo(HaveOccurred())

		_, err = db.Exec(`INSERT INTO backup_canary(id, job_id, backed_up_at) VALUES (1, 'job', ?)`, now.Format(time.RFC3339))
		Expect(err).NotTo(HaveOccurred())

		// The decryptor simply copies the plaintext DB to the output path.
		verifier = sqlitevault.NewVerifier(store, "unused").
			WithLogger(slog.New(slog.DiscardHandler)).
			WithDecryptor(func(in, out string) error {
				b, err := os.ReadFile(in)
				if err != nil {
					return err
				}
				return os.WriteFile(out, b, 0600)
			}).
			WithNowFunc(func() time.Time { return now })
	})

	Describe("VerifyLatest", func() {
		BeforeEach(func() {
			store.Put("e2e.hourly-latest.alias", []byte("e2e.hourly-09.db.age"))
			b, err := os.ReadFile(dbPath)
			Expect(err).NotTo(HaveOccurred())
			store.Put("e2e.hourly-09.db.age", b)
		})

		It("succeeds when the backup and canary are valid", func() {
			err := verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when the alias points to a missing object", func() {
			store.Put("e2e.hourly-latest.alias", []byte("e2e.missing.db.age"))
			err := verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).To(HaveOccurred())
		})

		It("fails when the decrypted file is not a valid SQLite database", func() {
			store.Put("e2e.hourly-09.db.age", []byte("not a database"))
			err := verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).To(HaveOccurred())
		})

		It("fails when the canary is missing", func() {
			// Recreate the decrypted DB without the canary table.
			tmpDir, err := os.MkdirTemp("", "verifier-no-canary-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, tmpDir)

			noCanaryPath := filepath.Join(tmpDir, "no-canary.db")
			noCanaryDB, err := sql.Open("sqlite", noCanaryPath)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(noCanaryDB.Close)

			_, err = noCanaryDB.Exec("CREATE TABLE other (id INTEGER PRIMARY KEY)")
			Expect(err).NotTo(HaveOccurred())

			b, err := os.ReadFile(noCanaryPath)
			Expect(err).NotTo(HaveOccurred())
			store.Put("e2e.hourly-09.db.age", b)

			err = verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reading canary"))
		})

		It("fails when the canary is too old", func() {
			later := now.Add(3 * time.Hour)
			verifier = verifier.WithNowFunc(func() time.Time { return later })

			// Use Verify (not VerifyLatest) with an empty slot so the max-age check is tested in isolation.
			err := verifier.Verify(context.Background(), "e2e.hourly-09.db.age", 2*time.Hour)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("too old"))
		})

		It("fails when the canary is outside the expected slot window", func() {
			// Move the reference time one hour later so the canary appears to be from the previous slot.
			later := now.Add(time.Hour)
			verifier = verifier.WithNowFunc(func() time.Time { return later })

			err := verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("outside expected backup window"))
		})

		It("fails when the decryptor fails", func() {
			verifier = verifier.WithDecryptor(func(in, out string) error {
				return errors.New("decryption failed on purpose")
			})

			err := verifier.VerifyLatest(context.Background(), "e2e", "hourly", 2*time.Hour)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("decrypting backup"))
		})
	})
})
