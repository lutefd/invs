package marketcalendar

import (
	"strings"
	"testing"
	"time"
)

const testSourceID = "5d6ac836-54fd-4df2-a745-0744180420db"

func TestCompileMaterializesWeekendsHolidayLateOpenAndEarlyClose(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	date := func(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, location) }
	definition := Definition{
		DataSourceID: testSourceID, MIC: "XNYS", ExchangeTimezone: "America/New_York",
		CalendarVersion: "xnys_2026_fixture_v1", CoverageStart: date(25), CoverageEnd: date(30),
		RegularOpenLocal: "09:30", RegularCloseLocal: "16:00",
		AvailableAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), RecordedAt: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		SourceReference: "fixture/evidence", RawPayloadHash: strings.Repeat("a", 64), Revision: 1,
		Events: []Event{
			{Date: date(26), Status: "closed", SourceReference: "thanksgiving"},
			{Date: date(27), Status: "open", CloseLocal: "13:00", SourceReference: "day_after_thanksgiving"},
			{Date: date(30), Status: "open", OpenLocal: "10:00", SourceReference: "late_open_fixture"},
		},
	}
	batch, err := Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Calendars) != 1 || len(batch.Sessions) != 6 || batch.Calendars[0].SessionCount != 6 {
		t.Fatalf("compiled batch = %+v", batch)
	}
	byDate := make(map[string]int)
	for index, session := range batch.Sessions {
		byDate[session.SessionDate] = index
	}
	thanksgiving := batch.Sessions[byDate["2026-11-26"]]
	if thanksgiving.SessionStatus != "closed" || thanksgiving.OpenAt != nil || thanksgiving.CloseAt != nil {
		t.Fatalf("holiday = %+v", thanksgiving)
	}
	weekend := batch.Sessions[byDate["2026-11-28"]]
	if weekend.SessionStatus != "closed" || !strings.Contains(weekend.SourceReference, "weekend_policy") {
		t.Fatalf("weekend = %+v", weekend)
	}
	early := batch.Sessions[byDate["2026-11-27"]]
	if !early.IsEarlyClose || early.CloseAt == nil || early.CloseAt.Format(time.RFC3339) != "2026-11-27T18:00:00Z" {
		t.Fatalf("early close = %+v", early)
	}
	late := batch.Sessions[byDate["2026-11-30"]]
	if late.IsEarlyClose || late.OpenAt == nil || late.OpenAt.Format(time.RFC3339) != "2026-11-30T15:00:00Z" {
		t.Fatalf("late open = %+v", late)
	}
}

func TestCompileFailsClosedOnAmbiguousOrInvalidEvidence(t *testing.T) {
	base := Definition{
		DataSourceID: testSourceID, MIC: "BVMF", ExchangeTimezone: "America/Sao_Paulo",
		CalendarVersion: "bvmf_2026_fixture_v1", CoverageStart: time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC), CoverageEnd: time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC),
		RegularOpenLocal: "10:00", RegularCloseLocal: "17:00", AvailableAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), RecordedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SourceReference: "fixture/evidence", RawPayloadHash: strings.Repeat("b", 64), Revision: 1,
	}
	duplicate := base
	duplicate.Events = []Event{{Date: base.CoverageStart, Status: "closed"}, {Date: base.CoverageStart, Status: "open"}}
	if _, err := Compile(duplicate); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("error = %v, want duplicate date", err)
	}
	badHours := base
	badHours.Events = []Event{{Date: base.CoverageStart, Status: "open", OpenLocal: "25:00"}}
	if _, err := Compile(badHours); err == nil || !strings.Contains(err.Error(), "invalid open time") {
		t.Fatalf("error = %v, want invalid override", err)
	}
}
