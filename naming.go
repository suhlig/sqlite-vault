package sqlitevault

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
	return fmt.Sprintf("%s.%s%s", prefix, slotObjectPart(now), ext)
}

// LatestAliasName returns the alias object name for the latest backup of the given slot category.
// The slot category must be one of "hourly", "daily", "weekly", or "yearly".
func LatestAliasName(prefix, slot string) string {
	return fmt.Sprintf("%s.%s-latest.alias", prefix, slot)
}

// Slot returns the backup slot category for the given time: "hourly", "daily", "weekly", or "yearly".
func Slot(now time.Time) string {
	if now.Hour() == 4 {
		if now.Weekday() == time.Sunday {
			if lastSundayOfTheYear(now) {
				return "yearly"
			}
			return "weekly"
		}
		return "daily"
	}
	return "hourly"
}

func slotObjectPart(now time.Time) string {
	switch Slot(now) {
	case "yearly":
		return fmt.Sprintf("yearly-%04d", now.Year())
	case "weekly":
		_, isoWeek := now.ISOWeek()
		return fmt.Sprintf("weekly-%02d", isoWeek)
	case "daily":
		return fmt.Sprintf("daily-%s", now.Format("Monday"))
	default:
		return fmt.Sprintf("hourly-%02d", now.Hour())
	}
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
