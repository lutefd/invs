package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/metadata"
)

func main() {
	var (
		databaseURL       string
		artifactID        string
		featureSet        string
		featureSetVersion string
		jsonOutput        bool
		failOnIssues      bool
	)
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&artifactID, "artifact-id", "", "limit the report to one artifact UUID")
	flag.StringVar(&featureSet, "feature-set", "", "limit the report to one feature-set name")
	flag.StringVar(&featureSetVersion, "feature-set-version", "", "limit the report to one feature-set version")
	flag.BoolVar(&jsonOutput, "json", false, "write the complete report as JSON")
	flag.BoolVar(&failOnIssues, "fail-on-issues", false, "exit 1 when any catalog consistency issue is reported")
	flag.Parse()

	if strings.TrimSpace(databaseURL) == "" {
		fatal(errors.New("DATABASE_URL or --database-url is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repository, err := metadata.Open(ctx, databaseURL)
	if err != nil {
		fatal(fmt.Errorf("open metadata database: %w", err))
	}
	defer repository.Close()

	filter := metadata.FeatureArtifactCatalogFilter{
		ArtifactID:        artifactID,
		FeatureSet:        featureSet,
		FeatureSetVersion: featureSetVersion,
	}
	artifacts, err := repository.ListFeatureArtifactCatalog(ctx, filter)
	if err != nil {
		fatal(fmt.Errorf("read feature artifact catalog: %w", err))
	}
	report := metadata.BuildFeatureArtifactCatalogReport(time.Now().UTC(), filter, artifacts)
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fatal(fmt.Errorf("encode feature catalog report: %w", err))
		}
	} else {
		printText(os.Stdout, report)
	}
	if failOnIssues && report.Summary.InconsistentArtifacts > 0 {
		os.Exit(1)
	}
}

func printText(writer io.Writer, report metadata.FeatureArtifactCatalogReport) {
	fmt.Fprintf(writer, "feature catalog report version=%d generated_at=%s artifacts=%d\n", report.Version, report.GeneratedAt.Format(time.RFC3339), report.Summary.ArtifactCount)
	fmt.Fprintf(writer, "summary complete=%d partial=%d empty=%d inconsistent=%d requested=%d accepted=%d cataloged=%d rejected=%d rows=%d\n",
		report.Summary.CompleteArtifacts, report.Summary.PartialArtifacts, report.Summary.EmptyArtifacts,
		report.Summary.InconsistentArtifacts, report.Summary.RequestedPartitions,
		report.Summary.AcceptedPartitions, report.Summary.CatalogedPartitions,
		report.Summary.RejectedPartitions, report.Summary.RowCount,
	)
	for _, artifact := range report.Artifacts {
		fmt.Fprintf(writer, "artifact=%s feature_set=%s@%s status=%s coverage=%s decisions=%d universe=%d accepted=%d rejected=%d rows=%d\n",
			artifact.ArtifactID, artifact.FeatureSet, artifact.FeatureSetVersion, artifact.Status,
			artifact.CoverageStatus, artifact.DecisionPointCount, artifact.UniverseCount,
			artifact.AcceptedPartitions, artifact.RejectedPartitions, artifact.RowCount,
		)
		fmt.Fprintf(writer, "  output_manifest=%s sha256=%s\n", artifact.OutputManifestPath, artifact.OutputManifestSHA256)
		for _, coverage := range artifact.DecisionCoverage {
			fmt.Fprintf(writer, "  decision=%s expected=%d accepted=%d unaccounted=%d rows=%d\n",
				coverage.DecisionAt.Format(time.RFC3339), coverage.ExpectedPartitions,
				coverage.AcceptedPartitions, coverage.UnaccountedPartitions, coverage.AcceptedRowCount,
			)
		}
		for _, fitness := range artifact.InputFitness {
			fmt.Fprintf(writer, "  input_fitness=%s historical_fitness=%s availability_policy=%s\n", fitness.Dataset, fitness.HistoricalFitness, fitness.AvailabilityPolicy)
		}
		for _, input := range artifact.InputRefs {
			fmt.Fprintf(writer, "  input=%s path=%s sha256=%s\n", input.Kind, input.Path, input.SHA256)
		}
		for _, partition := range artifact.Partitions {
			fmt.Fprintf(writer, "  partition=%s decision=%s child=%s part=%s rows=%d\n",
				partition.SecurityID, partition.DecisionAt.Format(time.RFC3339),
				partition.ChildArtifactID, partition.PartPath, partition.RowCount,
			)
		}
		for _, issue := range artifact.Issues {
			fmt.Fprintf(writer, "  issue=%s\n", issue)
		}
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-feature-report: %v\n", err)
	os.Exit(2)
}
