package backup_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	backup "github.com/suhlig/sqlite-vault"

	_ "modernc.org/sqlite"
)

var _ = Describe("Service", func() {
	var (
		now   time.Time
		svc   *backup.Service
		store *fakeStore
	)

	BeforeEach(func() {
		dsn, err := initTestDatabase()
		Expect(err).NotTo(HaveOccurred())

		store = &fakeStore{}

		nullLogger := slog.New(slog.DiscardHandler)
		svc = backup.NewService(dsn, store).
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

		It("uploads one hourly object", func() {
			store.mu.Lock()
			defer store.mu.Unlock()

			Expect(store.calls).To(HaveLen(1))
		})

		It("uses a zero-padded hourly object name", func() {
			store.mu.Lock()
			defer store.mu.Unlock()

			Expect(filepath.Base(store.calls[0].object)).To(Equal("test.hourly-09.db.age"))
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

			It("stores one weekly object", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(store.calls).To(HaveLen(1))
			})

			It("uses weekly-WeekNN in the object name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[0].object)).To(Equal(fmt.Sprintf("test.weekly-%02d.db.age", isoWeek)))
			})
		})

		Context("on a Monday at 04:XX", func() {
			BeforeEach(func() {
				now = time.Date(2025, 1, 6, 4, 37, 0, 0, time.UTC)
			})

			It("stores one daily object", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(store.calls).To(HaveLen(1))
			})

			It("uses the weekday name in the daily object name", func() {
				store.mu.Lock()
				defer store.mu.Unlock()

				Expect(filepath.Base(store.calls[0].object)).To(Equal("test.daily-Monday.db.age"))
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
})
