package backup_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	backup "github.com/suhlig/sqlite-vault"
)

var _ = Describe("object naming", func() {
	DescribeTable("object naming",
		func(now time.Time, expectedName string) {
			objectName := backup.ObjectName("unit-test", now, ".zip")
			Expect(objectName).To(Equal(expectedName))
		},
		Entry("Monday afternoon in March", time.Date(2025, 3, 10, 15, 21, 0, 0, time.UTC), "unit-test.hourly-15.zip"),
		Entry("Sunday afternoon in March", time.Date(2025, 3, 16, 15, 21, 0, 0, time.UTC), "unit-test.hourly-15.zip"),
		Entry("Tuesday morning during daily time in March", time.Date(2025, 3, 11, 4, 37, 0, 0, time.UTC), "unit-test.daily-Tuesday.zip"),
		Entry("Sunday morning during daily time in March", time.Date(2025, 3, 9, 4, 37, 0, 0, time.UTC), "unit-test.weekly-10.zip"),
		Entry("last Sunday of 2024, during daily time", time.Date(2024, 12, 29, 4, 37, 0, 0, time.UTC), "unit-test.yearly-2024.zip"),
	)
})
