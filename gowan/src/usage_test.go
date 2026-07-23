// usage_test.go
package main

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestUsageFirstSampleIsBaseline(t *testing.T) {
	u := &dev_usage{}
	u.apply_sample(1000, 500, at("2026-07-23 10:00"))
	if u.TotalRX != 0 || u.TotalTX != 0 {
		t.Fatalf("first sample must only set a baseline, got rx=%d tx=%d", u.TotalRX, u.TotalTX)
	}
	if !u.Seen || u.LastRX != 1000 || u.LastTX != 500 {
		t.Fatalf("baseline not recorded: %+v", u)
	}
}

func TestUsageAccumulatesDeltas(t *testing.T) {
	u := &dev_usage{}
	u.apply_sample(1000, 500, at("2026-07-23 10:00"))
	u.apply_sample(1500, 800, at("2026-07-23 10:01"))
	u.apply_sample(2000, 900, at("2026-07-23 10:02"))

	if u.TotalRX != 1000 || u.TotalTX != 400 {
		t.Fatalf("want total rx=1000 tx=400, got rx=%d tx=%d", u.TotalRX, u.TotalTX)
	}
	if u.DayRX != 1000 || u.MonthRX != 1000 {
		t.Fatalf("day/month should match total within one period: %+v", u)
	}
}

func TestUsageCounterReset(t *testing.T) {
	u := &dev_usage{}
	u.apply_sample(5000, 5000, at("2026-07-23 10:00"))
	u.apply_sample(6000, 6000, at("2026-07-23 10:01")) // +1000/+1000
	// Counter reset (reboot): raw drops below last → count raw as delta.
	u.apply_sample(300, 200, at("2026-07-23 10:02"))

	if u.TotalRX != 1000+300 || u.TotalTX != 1000+200 {
		t.Fatalf("reset must count new raw as delta, got rx=%d tx=%d", u.TotalRX, u.TotalTX)
	}
}

func TestUsageDayRollover(t *testing.T) {
	u := &dev_usage{}
	u.apply_sample(0, 0, at("2026-07-23 23:59"))
	u.apply_sample(1000, 1000, at("2026-07-23 23:59")) // day1: +1000/+1000
	u.apply_sample(1500, 1500, at("2026-07-24 00:01")) // new day: +500/+500

	if u.DayKey != "2026-07-24" {
		t.Fatalf("day key should roll to 2026-07-24, got %s", u.DayKey)
	}
	if u.DayRX != 500 || u.DayTX != 500 {
		t.Fatalf("new day counter should be 500/500, got %d/%d", u.DayRX, u.DayTX)
	}
	if u.TotalRX != 1500 {
		t.Fatalf("total should span days: %d", u.TotalRX)
	}
	// The finished day must be archived in history.
	found := false
	for _, h := range u.History {
		if h.Day == "2026-07-23" && h.RX == 1000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("finished day not archived: %+v", u.History)
	}
}

func TestUsageMonthRollover(t *testing.T) {
	u := &dev_usage{}
	u.apply_sample(0, 0, at("2026-07-31 23:00"))
	u.apply_sample(2000, 0, at("2026-07-31 23:00")) // July: +2000
	u.apply_sample(3000, 0, at("2026-08-01 00:30")) // August: +1000

	if u.MonthKey != "2026-08" || u.MonthRX != 1000 {
		t.Fatalf("month should reset to 2026-08 with 1000, got %s/%d", u.MonthKey, u.MonthRX)
	}
	if u.TotalRX != 3000 {
		t.Fatalf("total must span months: %d", u.TotalRX)
	}
}

func TestUsageHistoryCapped(t *testing.T) {
	u := &dev_usage{}
	// Roll through more than the cap of distinct days.
	base := at("2026-01-01 00:00")
	for i := 0; i < usage_history_days+30; i++ {
		day := base.AddDate(0, 0, i)
		u.apply_sample(uint64(i*100), 0, day)
	}
	if len(u.History) > usage_history_days {
		t.Fatalf("history must be capped at %d, got %d", usage_history_days, len(u.History))
	}
}
