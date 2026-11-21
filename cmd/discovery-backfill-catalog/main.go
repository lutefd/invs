package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/luisdourado/invs/internal/metadata"
)

type archivedPriceManifest struct {
	Parts []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"parts"`
}

type pinnedCalendarSnapshot struct {
	Manifest struct {
		DataSourceID       string `json:"data_source_id"`
		MIC                string `json:"mic"`
		CalendarVersion    string `json:"calendar_version"`
		SessionCount       int    `json:"session_count"`
		SessionFingerprint string `json:"session_fingerprint"`
		AvailableAt        string `json:"available_at"`
	} `json:"manifest"`
	Sessions []json.RawMessage `json:"sessions"`
}

func main() {
	var databaseURL, manifestPath, discoveryRoot string
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	flag.StringVar(&manifestPath, "manifest", "", "retrospective discovery backfill manifest")
	flag.StringVar(&discoveryRoot, "discovery-root", "/data/research/discovery/retrospective-current-universe", "retrospective discovery artifact root")
	flag.Parse()
	if strings.TrimSpace(databaseURL) == "" || strings.TrimSpace(manifestPath) == "" {
		fatal(errors.New("DATABASE_URL and --manifest are required"))
	}

	registration, counts, err := loadBackfillRegistration(manifestPath, discoveryRoot)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository, err := metadata.Open(ctx, databaseURL)
	if err != nil {
		fatal(err)
	}
	defer repository.Close()
	result, err := repository.RegisterRetrospectiveDiscoveryBackfill(ctx, registration)
	if err != nil {
		fatal(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"action": "cataloged", "reconstruction_run_id": result.ReconstructionRunID,
		"registration_sha256": result.RegistrationSHA256, "already_present": result.AlreadyPresent,
		"manifest_path": registration.ManifestPath, "session_count": counts.Sessions,
		"ranking_row_count": counts.Rankings, "price_bar_count": counts.PriceBars,
	})
}

type artifactCounts struct {
	Sessions  int
	Rankings  int
	PriceBars int
}

func loadBackfillRegistration(manifestPath, discoveryRoot string) (metadata.DiscoveryBackfillRegistration, artifactCounts, error) {
	root, err := canonicalDirectory(discoveryRoot)
	if err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, fmt.Errorf("resolve discovery-root: %w", err)
	}
	manifestFile, relativeManifest, err := resolveInputManifest(root, manifestPath)
	if err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, err
	}
	manifestBytes, err := os.ReadFile(manifestFile)
	if err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, fmt.Errorf("read backfill manifest: %w", err)
	}
	var document metadata.DiscoveryBackfillManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, fmt.Errorf("decode backfill manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, err
	}
	digest := sha256.Sum256(manifestBytes)
	registration, err := metadata.ValidateDiscoveryBackfillRegistration(metadata.DiscoveryBackfillRegistration{
		Manifest: document, ManifestPath: relativeManifest, ManifestSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, err
	}
	if err := validateBackfillArtifacts(root, registration); err != nil {
		return metadata.DiscoveryBackfillRegistration{}, artifactCounts{}, err
	}
	counts := artifactCounts{Sessions: len(document.Sessions)}
	for _, session := range document.Sessions {
		counts.Rankings += len(session.Rankings)
		counts.PriceBars += len(session.PriceBars)
	}
	return registration, counts, nil
}

func validateBackfillArtifacts(discoveryRoot string, registration metadata.DiscoveryBackfillRegistration) error {
	manifest := registration.Manifest
	if err := verifyArtifactHash(discoveryRoot, manifest.Profile.SnapshotPath, manifest.Profile.ProfileSHA256, "profile snapshot"); err != nil {
		return err
	}
	calendarBytes, err := readArtifact(discoveryRoot, manifest.Calendar.SnapshotPath)
	if err != nil {
		return fmt.Errorf("read calendar snapshot %s: %w", manifest.Calendar.SnapshotPath, err)
	}
	if actual := digestBytes(calendarBytes); actual != manifest.Calendar.SnapshotSHA256 {
		return fmt.Errorf("calendar snapshot hash mismatch: %s", manifest.Calendar.SnapshotPath)
	}
	var calendar pinnedCalendarSnapshot
	if err := json.Unmarshal(calendarBytes, &calendar); err != nil {
		return fmt.Errorf("decode calendar snapshot %s: %w", manifest.Calendar.SnapshotPath, err)
	}
	calendarAvailableAt, err := normalizeArtifactTimestamp(calendar.Manifest.AvailableAt)
	if err != nil {
		return fmt.Errorf("calendar snapshot available_at is invalid: %w", err)
	}
	if calendar.Manifest.DataSourceID != manifest.Calendar.DataSourceID ||
		calendar.Manifest.MIC != manifest.Calendar.MIC ||
		calendar.Manifest.CalendarVersion != manifest.Calendar.CalendarVersion ||
		calendar.Manifest.SessionCount != manifest.Calendar.SessionCount ||
		calendar.Manifest.SessionFingerprint != manifest.Calendar.SessionFingerprint ||
		calendarAvailableAt != manifest.Calendar.AvailableAt ||
		len(calendar.Sessions) != manifest.Calendar.SessionCount {
		return errors.New("calendar snapshot metadata or raw session count differs from backfill manifest")
	}
	archivedByPath := make(map[string]archivedPriceManifest, len(manifest.PriceInputs.Manifests))
	for _, ref := range manifest.PriceInputs.Manifests {
		content, err := readArtifact(discoveryRoot, ref.Path)
		if err != nil {
			return fmt.Errorf("read archived price manifest %s: %w", ref.Path, err)
		}
		if actual := digestBytes(content); actual != ref.SHA256 {
			return fmt.Errorf("archived price manifest hash mismatch: %s", ref.Path)
		}
		var archived archivedPriceManifest
		if err := json.Unmarshal(content, &archived); err != nil {
			return fmt.Errorf("decode archived price manifest %s: %w", ref.Path, err)
		}
		listedParts := make(map[string]string, len(archived.Parts))
		for _, part := range archived.Parts {
			if err := validateRelativePath(part.Path); err != nil {
				return fmt.Errorf("archived manifest %s has an unsafe part path: %w", ref.Path, err)
			}
			if _, exists := listedParts[part.Path]; exists {
				return fmt.Errorf("archived manifest %s repeats part %s", ref.Path, part.Path)
			}
			listedParts[part.Path] = part.SHA256
		}
		for _, part := range ref.Parts {
			relativePart, err := relativeToManifestDirectory(ref.SourcePath, part.SourcePath)
			if err != nil {
				return fmt.Errorf("map source part %s to archived manifest %s: %w", part.SourcePath, ref.SourcePath, err)
			}
			if listedParts[relativePart] != part.SHA256 {
				return fmt.Errorf("archived manifest %s does not match selected source part %s", ref.Path, part.SourcePath)
			}
			if err := verifyArtifactHash(discoveryRoot, part.Path, part.SHA256, "archived price part"); err != nil {
				return err
			}
		}
		archivedByPath[ref.Path] = archived
	}
	for _, session := range manifest.Sessions {
		for _, bar := range session.PriceBars {
			if _, exists := archivedByPath[bar.ManifestPath]; !exists {
				return fmt.Errorf("price bar references unknown archived manifest %s", bar.ManifestPath)
			}
			// Origin paths are only compared with the archived copy. They are never
			// opened or treated as authoritative after publication.
			partFound := false
			for _, ref := range manifest.PriceInputs.Manifests {
				if ref.Path != bar.ManifestPath || ref.SourcePath != bar.SourceManifestPath || ref.SHA256 != bar.ManifestSHA256 {
					continue
				}
				for _, part := range ref.Parts {
					if part.Path == bar.PartPath && part.SourcePath == bar.SourcePartPath && part.SHA256 == bar.PartSHA256 {
						partFound = true
						break
					}
				}
			}
			if !partFound {
				return fmt.Errorf("price bar %s has inconsistent pinned manifest or part references", bar.SecurityID)
			}
		}
	}
	return nil
}

func relativeToManifestDirectory(manifestPath, partPath string) (string, error) {
	if err := validateRelativePath(manifestPath); err != nil {
		return "", err
	}
	if err := validateRelativePath(partPath); err != nil {
		return "", err
	}
	manifestDir := filepath.FromSlash(filepath.Dir(manifestPath))
	part := filepath.FromSlash(partPath)
	relative, err := filepath.Rel(manifestDir, part)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("part path escapes manifest directory")
	}
	return filepath.ToSlash(relative), nil
}

func verifyArtifactHash(root, relativePath, expectedHash, label string) error {
	content, err := readArtifact(root, relativePath)
	if err != nil {
		return fmt.Errorf("read %s %s: %w", label, relativePath, err)
	}
	if actual := digestBytes(content); actual != expectedHash {
		return fmt.Errorf("%s hash mismatch: %s", label, relativePath)
	}
	return nil
}

func readArtifact(root, relativePath string) ([]byte, error) {
	fullPath, err := resolveArtifactPath(root, relativePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact is not a regular file")
	}
	return os.ReadFile(fullPath)
}

func resolveArtifactPath(root, relativePath string) (string, error) {
	if err := validateRelativePath(relativePath); err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(relativePath))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !isChildPath(relative) {
		return "", errors.New("artifact escapes discovery-root")
	}
	return resolved, nil
}

func canonicalDirectory(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("root is not a directory")
	}
	return resolved, nil
}

func resolveInputManifest(root, value string) (string, string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", "", fmt.Errorf("resolve manifest path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve manifest path: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !isChildPath(relative) {
		return "", "", errors.New("manifest must be below --discovery-root")
	}
	return resolved, filepath.ToSlash(relative), nil
}

func validateRelativePath(value string) error {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
		return errors.New("path must be a safe slash-separated relative path")
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("path contains an unsafe component")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("path contains an unsafe component")
		}
	}
	return nil
}

func isChildPath(relative string) bool {
	return relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func normalizeArtifactTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("backfill manifest has trailing JSON content")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "invs-discovery-backfill-catalog: %v\n", err)
	os.Exit(2)
}
