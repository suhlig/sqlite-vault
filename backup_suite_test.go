package sqlitevault_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestService(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Backup Suite")
}

func fakeEncryptor(in, out string) error {
	b, readErr := os.ReadFile(in)
	if readErr != nil {
		return readErr
	}

	return os.WriteFile(out, append([]byte("ENC:"), b...), 0600)
}

type fakeStore struct {
	mu    sync.Mutex
	calls []struct {
		local  string
		object string
	}
	objects map[string][]byte
	digest  string
	err     error
}

func (f *fakeStore) Store(_ context.Context, localPath, objectName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, struct {
		local  string
		object string
	}{local: localPath, object: objectName})

	if f.err != nil {
		return "", f.err
	}

	b, err := os.ReadFile(localPath)
	if err != nil {
		return "", err
	}

	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	f.objects[objectName] = b

	if f.digest == "" {
		return "deadbeef", nil
	}

	return f.digest, nil
}

func (f *fakeStore) Retrieve(_ context.Context, objectName, localPath string) error {
	f.mu.Lock()
	b, ok := f.objects[objectName]
	f.mu.Unlock()

	if !ok {
		return fmt.Errorf("object %q not found", objectName)
	}

	return os.WriteFile(localPath, b, 0600)
}

// initTestDatabase returns the DSN of a new, already initialized test database, or an error
func initTestDatabase() (string, error) {
	// We intentionally use a file-backed database; in-memory would require a shared cache and a pinned open connection.
	tmpDir, err := os.MkdirTemp("", "svc-test-*")

	if err != nil {
		return "", err
	}

	dbPath := filepath.Join(tmpDir, "db.sqlite")
	dsn := "file:" + dbPath

	db, err := sql.Open("sqlite", dsn)

	if err != nil {
		return "", fmt.Errorf("open sqlite: %w", err)
	}

	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO t(name) VALUES ('alpha');
	`)

	if err != nil {
		return "", fmt.Errorf("init schema: %w", err)
	}

	return dsn, nil
}
