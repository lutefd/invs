package normalize

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

func TestManifestUsesContentNamedPartAndCanonicalMetadata(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, rowsWritten, err := w.WritePrices(securityID, []model.PriceBar{price(time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	if rowsWritten != 1 {
		t.Fatalf("rows written=%d", rowsWritten)
	}
	if filepath.Base(manifestPath) != ManifestFilename {
		t.Fatalf("returned path=%q", manifestPath)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(manifestPath), "data.parquet")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy data.parquet exists: %v", err)
	}

	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestVersion != ManifestVersion || manifest.SchemaVersion != model.SchemaVersion {
		t.Fatalf("manifest versions=%d/%q", manifest.ManifestVersion, manifest.SchemaVersion)
	}
	if manifest.NormalizerVersion != model.NormalizerVersion || manifest.GitCommit != w.gitCommit {
		t.Fatalf("manifest versions/commit=%q/%q", manifest.NormalizerVersion, manifest.GitCommit)
	}
	if manifest.Source != "yahoo" || manifest.DataSourceID != sourceID || manifest.IngestionRunID != runID {
		t.Fatalf("manifest provenance=%+v", manifest)
	}
	wantPartition := map[string]string{"dataset": "prices", "source": "yahoo", "security_id": securityID}
	if err := samePartitionIdentity(manifest.Partition, wantPartition); err != nil {
		t.Fatal(err)
	}
	if manifest.RowCount != 1 || len(manifest.Parts) != 1 || manifest.Parts[0].RowCount != 1 {
		t.Fatalf("manifest counts=%d parts=%+v", manifest.RowCount, manifest.Parts)
	}
	part := manifest.Parts[0]
	if !strings.HasPrefix(part.Path, PartFilenamePrefix) || filepath.Ext(part.Path) != PartFilenameSuffix || part.Path != contentPartFilename(part.SHA256) {
		t.Fatalf("part path=%q sha=%q", part.Path, part.SHA256)
	}
	actualHash, err := sha256File(filepath.Join(filepath.Dir(manifestPath), part.Path))
	if err != nil {
		t.Fatal(err)
	}
	if actualHash != part.SHA256 {
		t.Fatalf("part sha=%q manifest sha=%q", actualHash, part.SHA256)
	}
}

func TestManifestPublicationSyncsPartBeforeManifestRename(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	baseOps := defaultPublicationOps()
	events := make([]string, 0, 8)
	w.ops = publicationOps{
		rename: func(oldPath, newPath string) error {
			events = append(events, "rename:"+filepath.Base(newPath))
			return baseOps.rename(oldPath, newPath)
		},
		syncFile: func(path string) error {
			events = append(events, "sync-file:"+filepath.Base(path))
			return baseOps.syncFile(path)
		},
		syncDir: func(path string) error {
			events = append(events, "sync-dir:"+filepath.Base(path))
			return baseOps.syncDir(path)
		},
	}
	manifestPath, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	partName := manifest.Parts[0].Path
	partSync := indexOfEvent(events, "sync-file:"+partName)
	manifestRename := indexOfEvent(events, "rename:"+ManifestFilename)
	if partSync < 0 || manifestRename < 0 || partSync > manifestRename {
		t.Fatalf("events=%v", events)
	}
	partDirSync := indexOfEventAfter(events, "sync-dir:", partSync)
	if partDirSync < 0 || partDirSync > manifestRename {
		t.Fatalf("part directory was not synced before manifest rename: %v", events)
	}
}

func TestManifestPublicationAppendsNewRowsAndKeepsLineage(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2024, 1, 2, 20, 0, 0, 0, time.UTC)
	manifestPath, _, err := w.WritePrices(securityID, []model.PriceBar{price(firstAt)})
	if err != nil {
		t.Fatal(err)
	}
	firstManifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	firstPart := firstManifest.Parts[0]

	second := price(firstAt.Add(24 * time.Hour))
	manifestPath, rowsWritten, err := w.WritePrices(securityID, []model.PriceBar{second})
	if err != nil {
		t.Fatal(err)
	}
	if rowsWritten != 1 {
		t.Fatalf("rows written=%d, want 1", rowsWritten)
	}
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RowCount != 2 || len(manifest.Parts) != 2 {
		t.Fatalf("manifest counts=%d parts=%+v", manifest.RowCount, manifest.Parts)
	}
	if manifest.Parts[0] != firstPart {
		t.Fatalf("first part changed: got=%+v want=%+v", manifest.Parts[0], firstPart)
	}
	for _, part := range manifest.Parts {
		if _, err := os.Stat(filepath.Join(filepath.Dir(manifestPath), part.Path)); err != nil {
			t.Fatalf("manifest part %s missing: %v", part.Path, err)
		}
	}
	if err := w.ValidateExisting(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyDataParquetIsNeverTreatedAsCommitted(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := pricePath(root)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o750); err != nil {
		t.Fatal(err)
	}
	row, err := priceRow(price(time.Now().UTC().Add(-3 * time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if err := parquet.WriteFile(legacyPath, []PriceRow{row}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ValidateExisting(); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("ValidateExisting error=%v", err)
	}
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-3 * time.Hour))}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("WritePrices error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(legacyPath), ManifestFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest was published alongside legacy file: %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("legacy file was modified")
	}
}

func TestCommittedPartHashMismatchFailsClosed(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-3 * time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	partPath := manifestPartPath(t, manifestPath)
	part, err := os.OpenFile(partPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("tampered")); err != nil {
		_ = part.Close()
		t.Fatal(err)
	}
	if err := part.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.WritePrices(securityID, []model.PriceBar{price(time.Now().UTC().Add(-2 * time.Hour))}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("WritePrices error=%v", err)
	}
	if err := w.ValidateExisting(); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("ValidateExisting error=%v", err)
	}
}

func indexOfEvent(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

func indexOfEventAfter(events []string, prefix string, after int) int {
	for i := after + 1; i < len(events); i++ {
		if strings.HasPrefix(events[i], prefix) {
			return i
		}
	}
	return -1
}
