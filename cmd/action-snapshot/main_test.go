package main

import "testing"

func TestValidateInputs(t *testing.T) {
	decision, err := validateInputs(
		"postgres://example",
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"2026-08-24T03:42:04-03:00",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.Format("2006-01-02T15:04:05Z07:00"); got != "2026-08-24T06:42:04Z" {
		t.Fatalf("decision_at=%s", got)
	}
}

func TestValidateInputsRejectsMissingAndNonCanonicalValues(t *testing.T) {
	cases := []struct {
		name, databaseURL, dataSourceID, securityID, decisionAt string
	}{
		{"database", "", "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "2026-08-24T03:42:04Z"},
		{"source", "postgres://example", "not-a-uuid", "22222222-2222-4222-8222-222222222222", "2026-08-24T03:42:04Z"},
		{"security", "postgres://example", "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-22222222222A", "2026-08-24T03:42:04Z"},
		{"decision", "postgres://example", "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "2026-08-24"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateInputs(test.databaseURL, test.dataSourceID, test.securityID, test.decisionAt); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
