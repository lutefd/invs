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

type freezeInput struct {
	PredictionID string    `json:"prediction_id"`
	FrozenAt     time.Time `json:"frozen_at"`
}

type closeHypothesisInput struct {
	HypothesisID string `json:"hypothesis_id"`
	Invalidated  bool   `json:"invalidated"`
}

func main() {
	var databaseURL, operation, inputPath string
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&operation, "operation", "", "research operation")
	flag.StringVar(&inputPath, "input", "-", "strict JSON input path, or - for stdin")
	flag.Parse()

	if strings.TrimSpace(databaseURL) == "" {
		fatal(errors.New("DATABASE_URL or --database-url is required"))
	}
	if strings.TrimSpace(operation) == "" {
		fatal(errors.New("--operation is required"))
	}
	input, err := readInput(inputPath)
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

	result, err := dispatch(ctx, repository, operation, input)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", operation, err))
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fatal(fmt.Errorf("encode result: %w", err))
	}
}

func readInput(path string) ([]byte, error) {
	var reader io.Reader = os.Stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open input %s: %w", path, err)
		}
		defer file.Close()
		reader = file
	}
	return io.ReadAll(reader)
}

func decodeInput(input []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("decode input: multiple JSON values")
		}
		return fmt.Errorf("decode input: %w", err)
	}
	return nil
}

func dispatch(ctx context.Context, repository *metadata.Repository, operation string, input []byte) (any, error) {
	switch operation {
	case "create-entity":
		var value metadata.ResearchEntity
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchEntity(ctx, value)
	case "create-theme":
		var value metadata.ResearchTheme
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchTheme(ctx, value)
	case "theme-revision":
		var value metadata.ResearchThemeRevision
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AppendResearchThemeRevision(ctx, value)
	case "theme-membership":
		var value metadata.ResearchThemeMembership
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AppendResearchThemeMembership(ctx, value)
	case "create-relationship":
		var value metadata.ResearchRelationship
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchRelationship(ctx, value)
	case "relationship-revision":
		var value metadata.ResearchRelationshipRevision
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AppendResearchRelationshipRevision(ctx, value)
	case "create-document":
		var value metadata.ResearchDocument
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchDocument(ctx, value)
	case "link-document-entity":
		var value metadata.ResearchDocumentEntityLink
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		if err := repository.LinkResearchDocumentEntity(ctx, value); err != nil {
			return nil, err
		}
		return map[string]string{"status": "linked"}, nil
	case "raw-artifact":
		var value metadata.ResearchRawArtifact
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.RegisterResearchRawArtifact(ctx, value)
	case "text-artifact":
		var value metadata.ResearchDocumentTextArtifact
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.RegisterResearchTextArtifact(ctx, value)
	case "create-event-proposal":
		var value metadata.ResearchEventProposal
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchEventProposal(ctx, value)
	case "event-revision":
		var value metadata.ResearchEventProposalRevision
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AppendResearchEventProposalRevision(ctx, value)
	case "register-pack":
		var value metadata.ResearchEvidencePack
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.RegisterResearchEvidencePack(ctx, value)
	case "create-hypothesis":
		var value metadata.ResearchHypothesis
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchHypothesis(ctx, value)
	case "hypothesis-revision":
		var value metadata.ResearchHypothesisRevision
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AppendResearchHypothesisRevision(ctx, value)
	case "hypothesis-evidence":
		var value metadata.ResearchHypothesisEvidence
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.AddResearchHypothesisEvidence(ctx, value)
	case "create-prediction":
		var value metadata.ResearchPrediction
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.CreateResearchPrediction(ctx, value)
	case "freeze-prediction":
		var value freezeInput
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.FreezeResearchPrediction(ctx, value.PredictionID, value.FrozenAt)
	case "prediction":
		var value struct {
			PredictionID string `json:"prediction_id"`
		}
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.GetResearchPrediction(ctx, value.PredictionID)
	case "outcome":
		var value metadata.ResearchPredictionOutcome
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		return repository.RecordResearchPredictionOutcome(ctx, value)
	case "close-hypothesis":
		var value closeHypothesisInput
		if err := decodeInput(input, &value); err != nil {
			return nil, err
		}
		if err := repository.CloseResearchHypothesis(ctx, value.HypothesisID, value.Invalidated); err != nil {
			return nil, err
		}
		return map[string]string{"status": "closed"}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", operation)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-research: %v\n", err)
	os.Exit(2)
}
