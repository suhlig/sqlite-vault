package sqlitevault_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sqlitevault "github.com/suhlig/sqlite-vault/v2"
)

var _ = Describe("Scheduler", func() {
	It("passes UTC time to the backup function even when the system timezone is not UTC", func() {
		// Use a timezone that is not UTC so we can detect if the scheduler
		// forgets to normalize the time before invoking the callback.
		loc := time.FixedZone("UTC+2", 2*60*60)
		future := time.Now().Add(time.Second).In(loc)

		var received time.Time
		scheduler := sqlitevault.NewScheduler(func(_ context.Context, t time.Time) {
			received = t
		}).WithNowFunc(func() time.Time { return future })

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = scheduler.Start(ctx)
		}()

		Eventually(func() time.Time { return received }).WithTimeout(5 * time.Second).ShouldNot(BeZero())
		cancel()

		Expect(received.Location()).To(Equal(time.UTC))
		Expect(received.Hour()).To(Equal(future.UTC().Hour()))
	})
})
