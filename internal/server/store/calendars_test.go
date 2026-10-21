package store_test

import (
	"context"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestCalendarCRUDAndJobAttach(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	cal := &store.Calendar{Name: " us-holidays ", Dates: []string{"2026-12-25", "2026-12-25", " 2027-01-01 "}}
	if err := st.CreateCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if cal.Name != "us-holidays" {
		t.Fatalf("name %q", cal.Name)
	}
	got, err := st.GetCalendar(ctx, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dates) != 2 || got.Dates[0] != "2026-12-25" || got.Dates[1] != "2027-01-01" {
		t.Fatalf("dates %+v", got.Dates)
	}
	j := localJob("cal-job")
	j.CalendarID = &cal.ID
	created, err := st.CreateJob(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.CalendarID == nil || *created.CalendarID != cal.ID {
		t.Fatalf("job calendar %+v", created.CalendarID)
	}
	if err := st.DeleteCalendar(ctx, cal.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CalendarID != nil {
		t.Fatalf("fk should set null, got %v", reloaded.CalendarID)
	}
	bad := &store.Calendar{Name: "bad", Dates: []string{"12/25/2026"}}
	if err := st.CreateCalendar(ctx, bad); err == nil {
		t.Fatal("invalid date must fail")
	}
}
