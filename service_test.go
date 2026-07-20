package sqlitevault_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sqlitevault "github.com/suhlig/sqlite-vault/v2"

	_ "modernc.org/sqlite"
)

var _ = Describe("Service", func() {
	var (
		now   time.Time
		svc   *sqlitevault.Service
		store *fakeStore
	)

	BeforeEach(func() {
		dsn, err := initTestDatabase()
		Expect(err).NotTo(HaveOccurred())

		store = &fakeStore{}

		nullLogger := slog.New(slog.DiscardHandler)
		svc = sqlitevault.NewService(dsn, store).
			WithObjectPrefix("test").
			WithLogger(nullLogger)
	})

	JustBeforeEach(func(ctx SpecContext) {
		svc.BackupFunc(ctx, now)
	})

	Describe("hourly frequency", func() {
		BeforeEach(func() {
			now = time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday 09:00
			svc = svc.WithEncryptor(fakeEncryptor)
		})

		It("uploads the backup object and its alias", func() {
			store.mu.Lock()
			defer store.mu.Unlock()

			Expect(store.calls).To(HaveLen(2))
		})

		It("uses a zero-padded hourly object name for the backup", func() {
			store.mu.Lock()
			defer store.mu.Unlock()

			Expect(filepath.Base(store.calls[0].object)).To(Equal("test.hourly-09.db.age"))
		})

		It("uses the hourly-latest alias name", func() {
			store.mu.Lock()
			defer store.mu.Unlock()

			Expect(filepath.Base(store.calls[1].object)).To(Equal("test.hourly-latest.alias"))
		})
	})

	Describe("daily frequency", func() {
		BeforeEach(func() {
			svc = svc.WithEncryptor(fakeEncryptor)
		})

		Context("on a Sunday at 04:XX", func() {
			var isoWeek int

			BeforeEach(func() {
				now = time.Date(2025, 1, 5, 4, 37, 0, 0, time.UTC)
				_, isoWeek = now.ISOWeek()
			})

			It("stores the weekly backup object and its alias", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(store.calls).To(HaveLen(2))
			})

			It("uses weekly-WeekNN in the object name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[0].object)).To(Equal(fmt.Sprintf("test.weekly-%02d.db.age", isoWeek)))
			})

			It("uses the weekly-latest alias name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[1].object)).To(Equal("test.weekly-latest.alias"))
			})
		})

		Context("on a Monday at 04:XX", func() {
			BeforeEach(func() {
				now = time.Date(2025, 1, 6, 4, 37, 0, 0, time.UTC)
			})

			It("stores the daily backup object and its alias", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(store.calls).To(HaveLen(2))
			})

			It("uses the weekday name in the daily object name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[0].object)).To(Equal("test.daily-Monday.db.age"))
			})

			It("uses the daily-latest alias name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[1].object)).To(Equal("test.daily-latest.alias"))
			})
		})
	})

	Context("failing encryptor", func() {
		var capturedInPath string

		BeforeEach(func() {
			svc = svc.WithEncryptor(func(in, out string) error {
				capturedInPath = in

				return errors.New("test encryption failing on purpose")
			})
		})

		It("removes plaintext file when encryption fails", func() {
			_, statErr := os.Stat(capturedInPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Describe("WithCanary", func() {
		BeforeEach(func() {
			svc = svc.WithEncryptor(fakeEncryptor)
		})

		It("returns an error for an invalid table name", func() {
			for _, name := range []string{"not a valid table", "1starts-with-digit", "sqlite_reserved", "has-dashes", ""} {
				_, err := svc.WithCanary(name)
				Expect(err).To(HaveOccurred(), "expected error for table name %q", name)
			}
		})

		Context("with a valid table name", func() {
			BeforeEach(func() {
				var err error
				svc, err = svc.WithCanary("backup_canary")
				Expect(err).NotTo(HaveOccurred())

				now = time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			})

			It("stores the backup object and its alias", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(store.calls).To(HaveLen(2))
			})
		})
	})
})
