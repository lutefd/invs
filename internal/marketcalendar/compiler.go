// Package marketcalendar compiles explicit source evidence into the durable
// calendar-manifest and trading-session metadata boundary.
package marketcalendar

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/metadata"
)

var (
	localTimePattern       = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)
	calendarVersionPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
)

type Event struct {
	Date            time.Time
	Status          string
	OpenLocal       string
	CloseLocal      string
	SourceReference string
}

type Definition struct {
	DataSourceID      string
	MIC               string
	ExchangeTimezone  string
	CalendarVersion   string
	CoverageStart     time.Time
	CoverageEnd       time.Time
	RegularOpenLocal  string
	RegularCloseLocal string
	AvailableAt       time.Time
	RecordedAt        time.Time
	SourceReference   string
	RawPayloadHash    string
	Revision          int
	Events            []Event
}

// Compile creates an explicit row for every covered local date. Weekend rows
// are materialized as closed; readers never infer weekdays or holidays.
func Compile(definition Definition) (metadata.HistoricalTruthBatch, error) {
	definition = normalizeDefinition(definition)
	if err := validateDefinition(definition); err != nil {
		return metadata.HistoricalTruthBatch{}, err
	}
	location, _ := time.LoadLocation(definition.ExchangeTimezone)
	events := make(map[string]Event, len(definition.Events))
	for index, event := range definition.Events {
		// Provider calendar events carry a source-local date rather than an
		// instant. Preserve its Y-M-D components when assigning the exchange
		// timezone; converting a UTC midnight would shift western exchanges.
		date := time.Date(event.Date.Year(), event.Date.Month(), event.Date.Day(), 0, 0, 0, 0, location)
		if date.Before(definition.CoverageStart) || date.After(definition.CoverageEnd) {
			continue
		}
		key := date.Format(time.DateOnly)
		if _, duplicate := events[key]; duplicate {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("calendar event %d duplicates %s", index, key)
		}
		event.Date = date
		event.Status = strings.TrimSpace(event.Status)
		event.OpenLocal = strings.TrimSpace(event.OpenLocal)
		event.CloseLocal = strings.TrimSpace(event.CloseLocal)
		if event.Status != "closed" && event.Status != "open" && event.Status != "notice" {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("calendar event %d has unsupported status %q", index, event.Status)
		}
		if (event.Status == "closed" || event.Status == "notice") && (event.OpenLocal != "" || event.CloseLocal != "") {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("calendar event %d closed row cannot override hours", index)
		}
		if event.OpenLocal != "" && !localTimePattern.MatchString(event.OpenLocal) {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("calendar event %d has invalid open time %q", index, event.OpenLocal)
		}
		if event.CloseLocal != "" && !localTimePattern.MatchString(event.CloseLocal) {
			return metadata.HistoricalTruthBatch{}, fmt.Errorf("calendar event %d has invalid close time %q", index, event.CloseLocal)
		}
		events[key] = event
	}

	sessions := make([]metadata.TradingSession, 0, daysInclusive(definition.CoverageStart, definition.CoverageEnd))
	for date := definition.CoverageStart; !date.After(definition.CoverageEnd); date = date.AddDate(0, 0, 1) {
		dateText := date.Format(time.DateOnly)
		event, hasEvent := events[dateText]
		closed := date.Weekday() == time.Saturday || date.Weekday() == time.Sunday
		reason := "regular_session"
		if closed {
			reason = "weekend_policy"
		}
		if hasEvent {
			switch event.Status {
			case "closed":
				closed = true
			case "open":
				closed = false
			}
			reason = event.SourceReference
		}
		session := metadata.TradingSession{
			SchemaVersion:    metadata.HistoricalSchemaVersion,
			CalendarVersion:  definition.CalendarVersion,
			MIC:              definition.MIC,
			ExchangeTimezone: definition.ExchangeTimezone,
			SessionDate:      dateText,
			SessionStatus:    "closed",
			AvailableAt:      definition.AvailableAt,
			SourceReference:  definition.SourceReference + "/date=" + dateText + "/rule=" + nonEmpty(reason, "regular_session"),
			RecordedAt:       definition.RecordedAt,
			DataSourceID:     definition.DataSourceID,
			RawPayloadHash:   definition.RawPayloadHash,
			Revision:         definition.Revision,
		}
		if !closed {
			openLocal, closeLocal := definition.RegularOpenLocal, definition.RegularCloseLocal
			if event.OpenLocal != "" {
				openLocal = event.OpenLocal
			}
			if event.CloseLocal != "" {
				closeLocal = event.CloseLocal
			}
			openAt, err := localInstant(date, openLocal, location)
			if err != nil {
				return metadata.HistoricalTruthBatch{}, fmt.Errorf("session %s open: %w", dateText, err)
			}
			closeAt, err := localInstant(date, closeLocal, location)
			if err != nil {
				return metadata.HistoricalTruthBatch{}, fmt.Errorf("session %s close: %w", dateText, err)
			}
			if !closeAt.After(openAt) {
				return metadata.HistoricalTruthBatch{}, fmt.Errorf("session %s close must follow open", dateText)
			}
			session.SessionStatus = "open"
			session.OpenAt = &openAt
			session.CloseAt = &closeAt
			session.IsEarlyClose = closeLocal < definition.RegularCloseLocal
		}
		session.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
			"calendar-session", definition.DataSourceID, definition.MIC,
			definition.CalendarVersion, dateText, fmt.Sprint(definition.Revision),
		}, "/"))).String()
		sessions = append(sessions, session)
	}
	fingerprint, err := metadata.CalendarSessionFingerprint(sessions)
	if err != nil {
		return metadata.HistoricalTruthBatch{}, err
	}
	manifest := metadata.CalendarManifest{
		SchemaVersion:      metadata.HistoricalSchemaVersion,
		ID:                 uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{"calendar-manifest", definition.DataSourceID, definition.MIC, definition.CalendarVersion}, "/"))).String(),
		CalendarVersion:    definition.CalendarVersion,
		MIC:                definition.MIC,
		ExchangeTimezone:   definition.ExchangeTimezone,
		AvailableAt:        definition.AvailableAt,
		SourceReference:    definition.SourceReference,
		RecordedAt:         definition.RecordedAt,
		DataSourceID:       definition.DataSourceID,
		RawPayloadHash:     definition.RawPayloadHash,
		SessionFingerprint: fingerprint,
		SessionCount:       len(sessions),
	}
	batch := metadata.HistoricalTruthBatch{Calendars: []metadata.CalendarManifest{manifest}, Sessions: sessions}
	if err := metadata.ValidateHistoricalTruthBatch(batch); err != nil {
		return metadata.HistoricalTruthBatch{}, fmt.Errorf("compiled calendar: %w", err)
	}
	return batch, nil
}

func normalizeDefinition(definition Definition) Definition {
	definition.DataSourceID = strings.TrimSpace(definition.DataSourceID)
	definition.MIC = strings.ToUpper(strings.TrimSpace(definition.MIC))
	definition.ExchangeTimezone = strings.TrimSpace(definition.ExchangeTimezone)
	definition.CalendarVersion = strings.ToLower(strings.TrimSpace(definition.CalendarVersion))
	definition.RegularOpenLocal = strings.TrimSpace(definition.RegularOpenLocal)
	definition.RegularCloseLocal = strings.TrimSpace(definition.RegularCloseLocal)
	definition.AvailableAt = definition.AvailableAt.UTC().Truncate(time.Microsecond)
	definition.RecordedAt = definition.RecordedAt.UTC().Truncate(time.Microsecond)
	definition.SourceReference = strings.TrimSpace(definition.SourceReference)
	definition.RawPayloadHash = strings.TrimSpace(definition.RawPayloadHash)
	if location, err := time.LoadLocation(definition.ExchangeTimezone); err == nil {
		definition.CoverageStart = time.Date(definition.CoverageStart.Year(), definition.CoverageStart.Month(), definition.CoverageStart.Day(), 0, 0, 0, 0, location)
		definition.CoverageEnd = time.Date(definition.CoverageEnd.Year(), definition.CoverageEnd.Month(), definition.CoverageEnd.Day(), 0, 0, 0, 0, location)
	}
	return definition
}

func validateDefinition(definition Definition) error {
	if _, err := uuid.Parse(definition.DataSourceID); err != nil {
		return fmt.Errorf("calendar data source ID must be UUID: %w", err)
	}
	if len(definition.MIC) != 4 {
		return errors.New("calendar MIC must contain four characters")
	}
	if _, err := time.LoadLocation(definition.ExchangeTimezone); err != nil {
		return fmt.Errorf("calendar timezone: %w", err)
	}
	if !calendarVersionPattern.MatchString(definition.CalendarVersion) {
		return fmt.Errorf("invalid calendar version %q", definition.CalendarVersion)
	}
	if definition.CoverageStart.IsZero() || definition.CoverageEnd.IsZero() || definition.CoverageEnd.Before(definition.CoverageStart) {
		return errors.New("calendar coverage requires an ordered start and end")
	}
	if daysInclusive(definition.CoverageStart, definition.CoverageEnd) > 370 {
		return errors.New("calendar coverage cannot exceed 370 days")
	}
	if !localTimePattern.MatchString(definition.RegularOpenLocal) || !localTimePattern.MatchString(definition.RegularCloseLocal) || definition.RegularOpenLocal >= definition.RegularCloseLocal {
		return errors.New("calendar regular local hours are invalid")
	}
	if definition.AvailableAt.IsZero() || definition.RecordedAt.Before(definition.AvailableAt) {
		return errors.New("calendar availability and recording times are invalid")
	}
	if definition.SourceReference == "" {
		return errors.New("calendar source reference is required")
	}
	if len(definition.RawPayloadHash) != 64 {
		return errors.New("calendar raw payload SHA-256 is required")
	}
	if definition.Revision < 0 {
		return errors.New("calendar revision must be nonnegative")
	}
	return nil
}

func localInstant(date time.Time, clock string, location *time.Location) (time.Time, error) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := time.Date(date.Year(), date.Month(), date.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location)
	return local.UTC().Truncate(time.Microsecond), nil
}

func daysInclusive(start, end time.Time) int {
	count := 0
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		count++
	}
	return count
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

// SortEvents provides a deterministic helper for adapters that build event
// slices from independent source sections.
func SortEvents(events []Event) {
	sort.Slice(events, func(i, j int) bool {
		if !events[i].Date.Equal(events[j].Date) {
			return events[i].Date.Before(events[j].Date)
		}
		return events[i].SourceReference < events[j].SourceReference
	})
}
