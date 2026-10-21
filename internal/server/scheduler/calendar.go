package scheduler

import (
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

const calendarDateLayout = "2006-01-02"

// DateInJobTZ is the YYYY-MM-DD of when in the job timezone (UTC if the TZ is invalid).
func DateInJobTZ(when time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return when.In(loc).Format(calendarDateLayout)
}

// SkipScheduledFire reports whether a cron/interval occurrence should be skipped.
// One-off and empty calendars never skip.
func SkipScheduledFire(job *store.Job, dates []string, fireAt time.Time) bool {
	if job == nil {
		return false
	}
	return SkipFire(job.ScheduleType, job.Timezone, dates, fireAt)
}

// SkipFire is SkipScheduledFire for any schedule.
func SkipFire(scheduleType, timezone string, dates []string, fireAt time.Time) bool {
	if scheduleType != TypeCron && scheduleType != TypeInterval {
		return false
	}
	if len(dates) == 0 {
		return false
	}
	day := DateInJobTZ(fireAt, timezone)
	for _, d := range dates {
		if d == day {
			return true
		}
	}
	return false
}
