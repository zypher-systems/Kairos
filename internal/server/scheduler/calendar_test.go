package scheduler

import (
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func TestDateInJobTZ(t *testing.T) {
	// 2026-12-25 04:00 UTC is 2026-12-24 23:00 in America/New_York (EST).
	when := time.Date(2026, 12, 25, 4, 0, 0, 0, time.UTC)
	if got := DateInJobTZ(when, "America/New_York"); got != "2026-12-24" {
		t.Fatalf("ny %s", got)
	}
	if got := DateInJobTZ(when, "UTC"); got != "2026-12-25" {
		t.Fatalf("utc %s", got)
	}
}

func TestSkipScheduledFire(t *testing.T) {
	dates := []string{"2026-12-25"}
	fire := time.Date(2026, 12, 25, 14, 0, 0, 0, time.UTC)
	cronJob := &store.Job{ScheduleType: TypeCron, Timezone: "UTC"}
	if !SkipScheduledFire(cronJob, dates, fire) {
		t.Fatal("cron holiday must skip")
	}
	interval := &store.Job{ScheduleType: TypeInterval, Timezone: "UTC"}
	if !SkipScheduledFire(interval, dates, fire) {
		t.Fatal("interval holiday must skip")
	}
	oneoff := &store.Job{ScheduleType: TypeOneoff, Timezone: "UTC"}
	if SkipScheduledFire(oneoff, dates, fire) {
		t.Fatal("one-off must not skip")
	}
	if SkipScheduledFire(cronJob, dates, fire.Add(24*time.Hour)) {
		t.Fatal("next day must fire")
	}
	if SkipScheduledFire(cronJob, nil, fire) {
		t.Fatal("empty calendar must not skip")
	}
}
