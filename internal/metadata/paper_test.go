package metadata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testPaperRegistration() PaperAccountRegistration {
	return PaperAccountRegistration{
		Account: PaperAccount{
			AccountID:          "40000000-0000-4000-8000-000000000001",
			SchemaVersion:      PaperSchemaVersion,
			Name:               "equal weight paper",
			BaseCurrency:       "USD",
			StrategyName:       "equal_weight",
			StrategyVersion:    "1.0.0",
			StrategyParameters: json.RawMessage(`{"rebalance_frequency":"daily"}`),
			StartDate:          "2026-01-01",
			EndDate:            "2026-12-31",
			InputFingerprint:   strings.Repeat("1", 64),
			PolicyVersion:      "1.0.0",
			ApprovalMode:       "manual",
			SpecPath:           "paper/equal-weight/account.json",
			SpecSHA256:         strings.Repeat("2", 64),
			EngineVersion:      "python-paper-1.0.0",
			CreatedAt:          time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}
}

func TestPaperRegistrationNormalizesAndHashesOperationalMetadataDeterministically(t *testing.T) {
	registration := testPaperRegistration()
	normalized, err := registration.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Account.Status != "active" {
		t.Fatalf("default status = %q, want active", normalized.Account.Status)
	}
	firstHash, err := paperAccountRegistrationSHA256(normalized.Account)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Account.CreatedAt = normalized.Account.CreatedAt.Add(time.Hour)
	normalized.Account.RegisteredAt = time.Now().UTC()
	secondHash, err := paperAccountRegistrationSHA256(normalized.Account)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("operational timestamps changed registration hash: %s != %s", firstHash, secondHash)
	}
}

func TestPaperRegistrationRejectsUnsafeIdentity(t *testing.T) {
	cases := []struct {
		name string
		edit func(*PaperAccountRegistration)
		want string
	}{
		{"unsafe spec path", func(r *PaperAccountRegistration) { r.Account.SpecPath = "../account.json" }, "unsafe path"},
		{"invalid fitness-like policy", func(r *PaperAccountRegistration) { r.Account.PolicyVersion = "current" }, "invalid"},
		{"invalid account currency", func(r *PaperAccountRegistration) { r.Account.BaseCurrency = "usd" }, "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registration := testPaperRegistration()
			tc.edit(&registration)
			if _, err := registration.normalized(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("normalized error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPaperEventNormalizesDetailsAndHashesWithoutCreationTime(t *testing.T) {
	event, err := normalizePaperEvent(PaperAccountEvent{
		AccountID:        "40000000-0000-4000-8000-000000000001",
		Sequence:         1,
		EventID:          "50000000-0000-4000-8000-000000000001",
		IdempotencyKey:   "cash-deposit:40000000-0000-4000-8000-000000000001",
		EventAt:          time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		SessionDate:      "2026-01-01",
		EventType:        "cash_deposit",
		Currency:         "USD",
		QuantityDelta:    "0",
		AmountLocalDelta: "10000",
		AmountBaseDelta:  "10000",
		Details:          json.RawMessage(`{"reason":"initial_cash"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !paperHashPattern.MatchString(event.RecordHash) || string(event.Details) != `{"reason":"initial_cash"}` {
		t.Fatalf("normalized event = %+v", event)
	}
	changed := event
	changed.EventAt = changed.EventAt.Add(time.Hour)
	changedHash, err := paperAccountEventRecordHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == event.RecordHash {
		t.Fatal("event time did not participate in event hash")
	}
}

func TestPaperMetadataSQLIsAppendOnlyAndIdempotent(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO paper_accounts",
		"ON CONFLICT (account_id) DO NOTHING",
		"RETURNING registration_sha256",
	} {
		if !strings.Contains(insertPaperAccountSQL, fragment) {
			t.Fatalf("account SQL missing %q", fragment)
		}
	}
	if !strings.Contains(insertPaperAccountEventSQL, "INSERT INTO paper_account_events") {
		t.Fatal("event SQL does not insert paper account events")
	}
}
