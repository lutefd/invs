package metadata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResearchJSONValidationDefaultsAndRejectsWrongShape(t *testing.T) {
	got, err := requireResearchJSON(nil, "items", "array", false)
	if err != nil || string(got) != "[]" {
		t.Fatalf("empty array default = %s, %v; want []", got, err)
	}
	if _, err := requireResearchJSON(json.RawMessage(`{}`), "items", "array", false); err == nil {
		t.Fatal("object accepted where array was required")
	}
	if _, err := requireResearchJSON(json.RawMessage(`[]`), "items", "array", true); err == nil {
		t.Fatal("empty array accepted where non-empty array was required")
	}
}

func TestResearchEvidenceRefsRequireContentAddressedLocator(t *testing.T) {
	good, err := evidenceRefsJSON([]ResearchEvidenceRef{{
		Kind: "document-text", ID: "text-1", SHA256: strings.Repeat("a", 64), Locator: "page=1;section=capex",
	}})
	if err != nil || len(good) == 0 {
		t.Fatalf("valid evidence ref = %s, %v", good, err)
	}
	for name, ref := range map[string]ResearchEvidenceRef{
		"missing locator": {Kind: "document-text", ID: "text-1", SHA256: strings.Repeat("a", 64)},
		"invalid hash":    {Kind: "document-text", ID: "text-1", SHA256: "bad", Locator: "offset=0"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := evidenceRefsJSON([]ResearchEvidenceRef{ref}); err == nil {
				t.Fatal("invalid evidence ref was accepted")
			}
		})
	}
}

func TestResearchRecordHashIsDeterministic(t *testing.T) {
	value := struct {
		ID   string    `json:"id"`
		When time.Time `json:"when"`
	}{"entity-1", time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
	first, err := researchRecordHash(value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := researchRecordHash(value)
	if err != nil || first != second || !researchSHA256Pattern.MatchString(first) {
		t.Fatalf("deterministic research hash = %q, %q, %v", first, second, err)
	}
}

func TestResearchRelationshipVocabularyIsExplicit(t *testing.T) {
	if len(researchRelationshipTypes) != 9 {
		t.Fatalf("relationship vocabulary size = %d, want 9", len(researchRelationshipTypes))
	}
	for _, relationshipType := range []string{"SUPPLIES", "CUSTOMER_OF", "DEPENDS_ON", "BENEFITS_FROM", "EXPOSED_TO", "COMPETES_WITH", "CONSUMES", "PRODUCES", "INDICATOR_FOR"} {
		if _, ok := researchRelationshipTypes[relationshipType]; !ok {
			t.Fatalf("relationship type %q is missing", relationshipType)
		}
	}
}
