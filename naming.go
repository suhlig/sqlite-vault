package backup

import (
	"fmt"
	"time"
)

// ObjectName builds an storage object name like "<prefix>.daily-14<ext>".
// When in the fifth hour of the day (4:00 .. 4:59 a.m.), it will create a name following this algorithm:
// 1. On days other than Sunday, it uses the weekday name like "<prefix>.Monday<ext>" to create a daily backup _instead_ of the hourly.
// 1. On Sundays, it uses an ISO week-based name like "<prefix>.WeekNN<ext>" to create a weekly backup _instead_ of the daily or hourly.
// 1. On December 31st, it uses the four-digit year number for a name like "<prefix>.2024<ext>" to create a yearly backup _instead_ of the weekly, daily or hourly.
func ObjectName(prefix string, now time.Time, ext string) string {
	if now.Hour() == 4 {
		if now.Weekday() == time.Sunday {
			if lastSundayOfTheYear(now) {
				return fmt.Sprintf("%s.yearly-%04d%s", prefix, now.Year(), ext)
			}

			_, isoWeek := now.ISOWeek()
			return fmt.Sprintf("%s.weekly-%02d%s", prefix, isoWeek, ext)
		}

		return fmt.Sprintf("%s.daily-%s%s", prefix, now.Format("Monday"), ext)
	}

	return fmt.Sprintf("%s.hourly-%02d%s", prefix, now.Hour(), ext)
}

// lastSundayOfTheYear returns true if now is the last Sunday on or before December 31 of the given year
func lastSundayOfTheYear(now time.Time) bool {
	loc := now.Location()
	dec31 := time.Date(now.Year(), time.December, 31, 0, 0, 0, 0, loc)

	// offset determines how many days to subtract from Dec 31 to reach the previous Sunday (possibly zero)
	offset := (int(dec31.Weekday()) - int(time.Sunday) + 7) % 7
	lastSun := dec31.AddDate(0, 0, -offset)

	yNow, mNow, dNow := now.Date()
	yLastSunday, mLastSunday, dLastSunday := lastSun.Date()

	return yNow == yLastSunday && mNow == mLastSunday && dNow == dLastSunday
}
