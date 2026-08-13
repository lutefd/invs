package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/metadata"
	"github.com/luisdourado/invs/internal/reconcile"
)

func main() {
	var (
		dataRoot     string
		databaseURL  string
		jsonOutput   bool
		failOnIssues bool
	)
	flag.StringVar(&dataRoot, "data-root", envOr("INVS_DATA_DIR", "data"), "durable data root")
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL; required unless --filesystem-only is used")
	filesystemOnly := flag.Bool("filesystem-only", false, "skip PostgreSQL run reconciliation")
	flag.BoolVar(&jsonOutput, "json", false, "write the complete report as JSON")
	flag.BoolVar(&failOnIssues, "fail-on-issues", false, "exit 1 when any finding is reported")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var runs []metadata.ReconciliationRun
	if !*filesystemOnly {
		if strings.TrimSpace(databaseURL) == "" {
			fatal(logger, errors.New("DATABASE_URL is required unless --filesystem-only is used"))
		}
		repository, err := metadata.Open(ctx, databaseURL)
		if err != nil {
			fatal(logger, err)
		}
		defer repository.Close()
		runs, err = repository.ListRunsForReconciliation(ctx)
		if err != nil {
			fatal(logger, fmt.Errorf("list ingestion runs: %w", err))
		}
	}

	records := make([]reconcile.RunRecord, 0, len(runs))
	for _, run := range runs {
		records = append(records, reconcile.RunRecord{
			ID: run.ID, Source: run.Source, RunKey: run.RunKey, Status: run.Status,
			RawPayloadManifestHash: run.RawPayloadManifestHash,
		})
	}
	report, err := reconcile.Reconcile(ctx, reconcile.Roots{DataRoot: dataRoot}, records, time.Now().UTC())
	if err != nil {
		fatal(logger, err)
	}
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fatal(logger, err)
		}
	} else {
		printText(report)
	}
	if failOnIssues && report.Summary.TotalIssues > 0 {
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func printText(report reconcile.Report) {
	fmt.Printf("reconciliation report generated_at=%s issues=%d data_root=%s\n", report.GeneratedAt.Format(time.RFC3339), report.Summary.TotalIssues, report.Roots.DataRoot)
	printCount("queued_or_running", report.Summary.QueuedOrRunning)
	printCount("raw_manifests_without_terminal_run", report.Summary.RawManifestsWithoutTerminalRun)
	printCount("raw_manifest_errors", report.Summary.RawManifestErrors)
	printCount("raw_manifest_hash_mismatches", report.Summary.RawManifestHashMismatches)
	printCount("invalid_normalized_manifests", report.Summary.InvalidNormalizedManifests)
	printCount("unlisted_normalized_parts", report.Summary.UnlistedNormalizedParts)
	printCount("missing_normalized_parts", report.Summary.MissingNormalizedParts)
	printCount("normalized_part_hash_mismatches", report.Summary.NormalizedPartHashMismatches)
	printCount("feature_artifact_errors", report.Summary.FeatureArtifactErrors)
	printCount("feature_artifacts_with_unavailable_inputs", report.Summary.FeatureArtifactsWithUnavailableInputs)
	for _, finding := range report.QueuedOrRunning {
		fmt.Printf("  active run: %s %s %s status=%s\n", finding.Source, finding.RunKey, finding.ID, finding.Status)
	}
	for _, finding := range report.RawManifestsWithoutTerminalRun {
		fmt.Printf("  raw without terminal run: %s (%s/%s status=%s)\n", finding.Path, finding.Source, finding.RunID, finding.Status)
	}
	for _, finding := range report.UnlistedNormalizedParts {
		fmt.Printf("  unlisted normalized part: %s\n", finding.Path)
	}
	for _, finding := range report.MissingNormalizedParts {
		fmt.Printf("  missing normalized part: %s expected=%s\n", finding.Path, finding.Expected)
	}
	for _, finding := range report.FeatureArtifactsWithUnavailableInputs {
		fmt.Printf("  feature inputs unavailable: %s\n", finding.Path)
		for _, input := range finding.Inputs {
			fmt.Printf("    %s %s: %s\n", input.Kind, input.Path, input.Detail)
		}
	}
}

func printCount(name string, count int) {
	if count > 0 {
		fmt.Printf("%s=%d\n", name, count)
	}
}

func fatal(logger *slog.Logger, err error) {
	logger.Error("reconciliation failed", "error", err)
	os.Exit(2)
}
