package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/metadata"
)

type snapshot struct {
	SchemaVersion string                            `json:"schema_version"`
	DataSourceID  string                            `json:"data_source_id"`
	SecurityID    string                            `json:"security_id"`
	DecisionAt    time.Time                         `json:"decision_at"`
	Actions       []metadata.CorporateActionVersion `json:"actions"`
}

func main() {
	var databaseURL, dataSourceID, securityID, decisionText string
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&dataSourceID, "data-source-id", "", "explicit action data-source UUID")
	flag.StringVar(&securityID, "security-id", "", "security UUID")
	flag.StringVar(&decisionText, "decision-at", "", "RFC 3339 knowledge cutoff")
	flag.Parse()

	decisionAt, err := validateInputs(databaseURL, dataSourceID, securityID, decisionText)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository, err := metadata.Open(ctx, databaseURL)
	if err != nil {
		fatal(fmt.Errorf("open metadata database: %w", err))
	}
	defer repository.Close()
	actions, err := repository.ResolveCorporateActions(ctx, dataSourceID, securityID, decisionAt)
	if err != nil {
		fatal(fmt.Errorf("resolve corporate actions: %w", err))
	}
	if actions == nil {
		actions = []metadata.CorporateActionVersion{}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot{
		SchemaVersion: "1.0.0",
		DataSourceID:  dataSourceID,
		SecurityID:    securityID,
		DecisionAt:    decisionAt.UTC(),
		Actions:       actions,
	}); err != nil {
		fatal(fmt.Errorf("encode action snapshot: %w", err))
	}
}

func validateInputs(databaseURL, dataSourceID, securityID, decisionText string) (time.Time, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return time.Time{}, errors.New("DATABASE_URL or --database-url is required")
	}
	for name, value := range map[string]string{
		"data-source-id": dataSourceID,
		"security-id":    securityID,
	} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed.String() != value {
			return time.Time{}, fmt.Errorf("%s must be a canonical UUID", name)
		}
	}
	decisionAt, err := time.Parse(time.RFC3339Nano, decisionText)
	if err != nil {
		return time.Time{}, errors.New("decision-at must be RFC 3339")
	}
	return decisionAt.UTC(), nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-action-snapshot: %v\n", err)
	os.Exit(2)
}
