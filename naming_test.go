package sqlitevault_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sqlitevault "github.com/suhlig/sqlite-vault"
)

var _ = Describe("Naming", func() {
	Describe("ObjectName", func() {
		It("uses the weekday name for daily backups", func() {
			now := time.Date(2025, 1, 6, 4, 37, 0, 0, time.UTC) // Monday 04:37
			Expect(sqlitevault.ObjectName("test", now, ".db.age")).To(Equal("test.daily-Monday.db.age"))
		})

		It("uses the ISO week for weekly backups", func() {
			now := time.Date(2025, 1, 5, 4, 37, 0, 0, time.UTC) // Sunday 04:37
			Expect(sqlitevault.ObjectName("test", now, ".db.age")).To(Equal("test.weekly-01.db.age"))
		})

		It("uses the year for yearly backups", func() {
			// 2024-12-29 is the last Sunday of 2024.
			now := time.Date(2024, 12, 29, 4, 37, 0, 0, time.UTC)
			Expect(sqlitevault.ObjectName("test", now, ".db.age")).To(Equal("test.yearly-2024.db.age"))
		})

		It("uses a zero-padded hour for hourly backups", func() {
			now := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			Expect(sqlitevault.ObjectName("test", now, ".db.age")).To(Equal("test.hourly-09.db.age"))
		})
	})

	Describe("Slot", func() {
		It("returns hourly for non-4am hours", func() {
			now := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			Expect(sqlitevault.Slot(now)).To(Equal("hourly"))
		})

		It("returns daily for 4am on a weekday", func() {
			now := time.Date(2025, 1, 6, 4, 0, 0, 0, time.UTC) // Monday
			Expect(sqlitevault.Slot(now)).To(Equal("daily"))
		})

		It("returns weekly for 4am on a regular Sunday", func() {
			now := time.Date(2025, 1, 5, 4, 0, 0, 0, time.UTC) // Sunday
			Expect(sqlitevault.Slot(now)).To(Equal("weekly"))
		})

		It("returns yearly for 4am on the last Sunday of the year", func() {
			now := time.Date(2024, 12, 29, 4, 0, 0, 0, time.UTC)
			Expect(sqlitevault.Slot(now)).To(Equal("yearly"))
		})
	})

	Describe("LatestAliasName", func() {
		It("builds an alias name from a prefix and slot", func() {
			Expect(sqlitevault.LatestAliasName("myapp", "daily")).To(Equal("myapp.daily-latest.alias"))
		})
	})
})
