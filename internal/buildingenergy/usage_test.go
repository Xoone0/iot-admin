package buildingenergy

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestUsageDistinguishesZeroMissingAndReset(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	at := func(hour int) time.Time { return start.Add(time.Duration(hour) * time.Hour) }
	readings := []Sample{
		readFixture(at(6), 9, "t6"),
		readFixture(at(0), 1000, "t0"),
		readFixture(at(1), 1040, "t1"),
		readFixture(at(1), 1040, "t1"),
		readFixture(at(2), 1040, "t2"),
		readFixture(at(4), 1060, "t4"),
		readFixture(at(5), 2, "t5"),
	}
	readings[0].Epoch, readings[6].Epoch = "new", "new"
	profile := pointFixture(start, Electricity, Cumulative, "kWh")
	profile.Multiplier = 2
	got, err := Calculate(MeteringData{Profile: profile, Samples: readings}, fixtureWindow(at(0), at(6), Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 6 || got.Valid != 3 || got.Expected != 6 || got.Missing != 2 || got.Suspect != 1 ||
		got.Usage == nil || *got.Usage != 94 || !got.LatestEnd.Equal(at(6)) {
		t.Fatalf("unexpected summary: %+v", got)
	}
	if b := got.Buckets[0]; b.Usage == nil || *b.Usage != 80 {
		t.Fatalf("first interval: %+v", b)
	}
	if b := got.Buckets[1]; b.Usage == nil || *b.Usage != 0 {
		t.Fatalf("known zero: %+v", b)
	}
	if b := got.Buckets[2]; b.Usage != nil || b.Missing != 1 {
		t.Fatalf("missing interval: %+v", b)
	}
	if b := got.Buckets[4]; b.Usage != nil || b.Suspect != 1 {
		t.Fatalf("reset interval: %+v", b)
	}
}

func TestUsageConflictingSamplesAndConfirmedRollover(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	at := func(hour int) time.Time { return from.Add(time.Duration(hour) * time.Hour) }
	profile := pointFixture(from, Electricity, Cumulative, "kWh")
	profile.Modulus = 1000
	readings := []Sample{
		readFixture(at(0), 990, "t0"), readFixture(at(1), 5, "t1"),
		readFixture(at(2), 8, "t2"), readFixture(at(2), 9, "conflict"), readFixture(at(3), 20, "t3"),
		readFixture(at(4), 2, "t4"), readFixture(at(5), 6, "t5"),
	}
	readings[1].Rollover, readings[6].Quality = true, Suspect
	got, err := Calculate(MeteringData{Profile: profile, Samples: readings}, fixtureWindow(from, at(5), Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Usage == nil || *got.Usage != 15 || got.Valid != 1 || got.Suspect != 4 || !got.LatestEnd.Equal(at(1)) {
		t.Fatalf("conflict, unknown rollback or suspect samples counted: %+v", got)
	}
}

func TestUsageDailyCalendarHandlesShortAndLongLocalDays(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		month time.Month
		day   int
		hours int
	}{
		{time.March, 8, 23},
		{time.November, 1, 25},
	} {
		from := time.Date(2026, tc.month, tc.day, 0, 0, 0, 0, location)
		to := from.AddDate(0, 0, 1)
		readings := make([]Sample, tc.hours+1)
		for index := range readings {
			at := from.Add(time.Duration(index) * time.Hour)
			readings[index] = readFixture(at, float64(100+index), at.Format(time.RFC3339))
		}
		profile := pointFixture(from, Electricity, Cumulative, "kWh")
		got, err := Calculate(MeteringData{Profile: profile, Samples: readings}, fixtureWindow(from, to, Day))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Buckets) != 1 || got.Expected != tc.hours || got.Valid != tc.hours || got.Usage == nil ||
			*got.Usage != float64(tc.hours) || !got.Buckets[0].To.Equal(to) {
			t.Fatalf("local day %s: %+v", from, got)
		}
		t.Logf("%s: actual %g kWh, %d local-day hours", from.Format("2006-01-02"), *got.Usage, got.Expected)
	}
}

func TestUsageMonthlyCoverageAndInvalidWindows(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, location)
	end := start.AddDate(0, 1, 0)
	data := MeteringData{Profile: pointFixture(start, Electricity, Cumulative, "kWh")}
	got, err := Calculate(data, fixtureWindow(start, end, Month))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 1 || got.Usage != nil || got.LatestEnd != nil ||
		got.Expected != 744 || got.Missing != 744 || got.Buckets[0].Usage != nil {
		t.Fatalf("empty month: %+v", got)
	}
	if _, err := Calculate(data, fixtureWindow(start, end.Add(-time.Hour), Month)); err == nil {
		t.Fatal("accepted a non-aligned monthly window")
	}
	data.Profile.Multiplier = 0
	if _, err := Calculate(data, fixtureWindow(start, end, Month)); err == nil {
		t.Fatal("accepted an unknown multiplier")
	}
}
