package backup_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"filippo.io/age"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	backup "github.com/suhlig/sqlite-vault"
	_ "modernc.org/sqlite"
)

type memoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: make(map[string][]byte)}
}

func (m *memoryStore) Store(_ context.Context, localPath, objectName string) (string, error) {
	b, err := os.ReadFile(localPath)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.objects[objectName] = b
	m.mu.Unlock()

	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:]), nil
}

func (m *memoryStore) Get(name string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[name]
	return b, ok
}

var _ = Describe("End-to-end backup, encryption, upload, download, decrypt, restore", func() {
	var (
		store                 *memoryStore
		svc                   *backup.Service
		now                   time.Time
		originalSentinelValue string
		err                   error
		dbDir                 string
	)

	BeforeEach(func() {
		dbDir, err = os.MkdirTemp("", "e2e-backup-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dbDir)

		dsn := "file:" + filepath.Join(dbDir, "db.sqlite")

		db, err := sql.Open("sqlite", dsn)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		_, err = db.Exec("CREATE TABLE sentinel (value TEXT)")
		Expect(err).NotTo(HaveOccurred())

		originalSentinelValue = fmt.Sprintf("sentinel-%d", time.Now().UnixNano())

		_, err = db.Exec("INSERT INTO sentinel(value) VALUES (?)", originalSentinelValue)
		Expect(err).NotTo(HaveOccurred())

		store = newMemoryStore()
		// Configure the service with real age scrypt encryption and a memory object store.
		svc = backup.NewService(dsn, store).WithObjectPrefix("e2e").WithLogger(slog.New(slog.DiscardHandler))

		svc, err = svc.WithPassphrase("test-passphrase")
		Expect(err).NotTo(HaveOccurred())
	})

	JustBeforeEach(func() {
		svc.BackupFunc(context.Background(), now)
	})

	Context("at a time that yields an hourly object name", func() {
		BeforeEach(func() {
			now = time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday 09:00 UTC
		})

		Describe("the downloaded, decrypted and attached backup", func() {
			var memDB *sql.DB

			JustBeforeEach(func() {
				var (
					encryptedBytes []byte
					decryptedFile  *os.File
					decryptedPath  string
				)

				By("downloading the cncrypted file", func() {
					var result bool
					encryptedBytes, result = store.Get(backup.ObjectName("e2e", now, ".db.age"))
					Expect(result).To(BeTrue())

					tmpDir, err := os.MkdirTemp("", "e2e-backup-*")
					Expect(err).NotTo(HaveOccurred())

					decryptedPath = filepath.Join(tmpDir, "restored.sqlite")
					decryptedFile, err = os.Create(decryptedPath)
					Expect(err).NotTo(HaveOccurred())
				})

				By("decrypting the downloaded file", func() {
					identity, err := age.NewScryptIdentity("test-passphrase")
					Expect(err).NotTo(HaveOccurred())

					decryptedReader, err := age.Decrypt(bytes.NewReader(encryptedBytes), identity)
					Expect(err).NotTo(HaveOccurred())

					_, err = io.Copy(decryptedFile, decryptedReader)
					Expect(err).NotTo(HaveOccurred())
					Expect(decryptedFile.Close()).To(Succeed())
				})

				By("attaching the decrypted file to a new in-memory database", func() {
					memDB, err = sql.Open("sqlite", "file:memdb_e2e?mode=memory&cache=shared")
					Expect(err).NotTo(HaveOccurred())
					DeferCleanup(memDB.Close)

					_, err = memDB.Exec("ATTACH DATABASE ? AS restored", decryptedPath)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Describe("reading the restored sentinel value", func() {
				var restoredSentinelValue string

				JustBeforeEach(func() {
					err = memDB.QueryRow("SELECT value FROM restored.sentinel LIMIT 1").Scan(&restoredSentinelValue)
				})

				It("can be read back", func() {
					Expect(err).NotTo(HaveOccurred())
				})

				It("contains the original sentinel value", func() {
					Expect(restoredSentinelValue).To(Equal(originalSentinelValue))
				})
			})
		})
	})
})
