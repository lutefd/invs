package normalize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"github.com/luisdourado/invs/internal/model"
)

// The manifest is the only committed reader pointer for one normalized
// partition. Readers must open manifest.json, verify each listed part's
// sha256, and read only the listed parts. Other Parquet files in the
// partition, including data.parquet, are not committed data.
const (
	ManifestFilename   = "manifest.json"
	ManifestVersion    = 1
	PartFilenamePrefix = "part-"
	PartFilenameSuffix = ".parquet"
	UnknownGitCommit   = "unknown"
)

// Manifest is the JSON contract consumed by downstream readers. A manifest
// describes one complete canonical snapshot of one partition. Its shape is:
//
//	{"manifest_version":1,"schema_version":"1.0.0","normalizer_version":"go-v1","git_commit":"<sha1-or-unknown>","source":"<source>","data_source_id":"<uuid>","ingestion_run_id":"<uuid>","partition":{"dataset":"<dataset>","source":"<source>","<key>":"<value>"},"row_count":1,"parts":[{"path":"part-<sha256>.parquet","sha256":"<sha256>","row_count":1}]}
//
// The Go writer appends newly accepted canonical rows as content-named parts;
// each manifest lists the complete current part set so readers retain stable
// lineage to earlier publications.
type Manifest struct {
	ManifestVersion   int               `json:"manifest_version"`
	SchemaVersion     string            `json:"schema_version"`
	NormalizerVersion string            `json:"normalizer_version"`
	GitCommit         string            `json:"git_commit"`
	Source            string            `json:"source"`
	DataSourceID      string            `json:"data_source_id"`
	IngestionRunID    string            `json:"ingestion_run_id"`
	Partition         map[string]string `json:"partition"`
	RowCount          int               `json:"row_count"`
	Parts             []ManifestPart    `json:"parts"`
}

// ManifestPart identifies an immutable Parquet file relative to the
// manifest's directory. Its filename embeds the same lower-case SHA-256 that
// is repeated in SHA256 for cheap path and metadata validation.
type ManifestPart struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	RowCount int    `json:"row_count"`
}

type publicationMetadata struct {
	Source            string
	DataSourceID      string
	IngestionRunID    string
	NormalizerVersion string
}

type publicationOps struct {
	rename   func(string, string) error
	syncFile func(string) error
	syncDir  func(string) error
}

func defaultPublicationOps() publicationOps {
	return publicationOps{
		rename:   os.Rename,
		syncFile: syncFilePath,
		syncDir:  syncDirectoryPath,
	}
}

func (w *Writer) publicationOps() publicationOps {
	ops := w.ops
	defaults := defaultPublicationOps()
	if ops.rename == nil {
		ops.rename = defaults.rename
	}
	if ops.syncFile == nil {
		ops.syncFile = defaults.syncFile
	}
	if ops.syncDir == nil {
		ops.syncDir = defaults.syncDir
	}
	return ops
}

// ReadManifest reads and structurally validates one manifest. It does not
// read the referenced parts; a reader must verify those files before use.
func ReadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("decode manifest %s: multiple JSON values", path)
		}
		return Manifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("validate manifest %s: %w", path, err)
	}
	return manifest, nil
}

func readManifestIfPresent(path string) (Manifest, bool, error) {
	manifest, err := ReadManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, true, err
	}
	return manifest, true, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.ManifestVersion != ManifestVersion {
		return fmt.Errorf("unsupported manifest_version %d", manifest.ManifestVersion)
	}
	if manifest.SchemaVersion != model.SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", manifest.SchemaVersion)
	}
	if manifest.NormalizerVersion == "" {
		return errors.New("normalizer_version required")
	}
	if !validGitCommit(manifest.GitCommit) {
		return errors.New("git_commit must be a full lower-case SHA-1 or unknown")
	}
	if manifest.Source == "" {
		return errors.New("source required")
	}
	if _, err := uuid.Parse(manifest.DataSourceID); err != nil {
		return errors.New("data_source_id must be UUID")
	}
	if _, err := uuid.Parse(manifest.IngestionRunID); err != nil {
		return errors.New("ingestion_run_id must be UUID")
	}
	if err := validatePartitionIdentity(manifest.Partition); err != nil {
		return err
	}
	if manifest.Partition["source"] != manifest.Source {
		return errors.New("partition source does not match source")
	}
	if manifest.RowCount < 0 {
		return errors.New("row_count must not be negative")
	}
	if len(manifest.Parts) == 0 {
		return errors.New("parts must not be empty")
	}

	rowCount := 0
	seen := make(map[string]struct{}, len(manifest.Parts))
	for _, part := range manifest.Parts {
		if !validSHA256(part.SHA256) {
			return fmt.Errorf("part %q has invalid sha256", part.Path)
		}
		if part.RowCount < 0 {
			return fmt.Errorf("part %q row_count must not be negative", part.Path)
		}
		if part.Path == "" || filepath.Base(part.Path) != part.Path || strings.ContainsAny(part.Path, "/\\\x00") {
			return fmt.Errorf("part path %q must be a relative filename", part.Path)
		}
		if part.Path == "data.parquet" || part.Path != contentPartFilename(part.SHA256) {
			return fmt.Errorf("part path %q is not content-named", part.Path)
		}
		if _, ok := seen[part.Path]; ok {
			return fmt.Errorf("duplicate part path %q", part.Path)
		}
		seen[part.Path] = struct{}{}
		if rowCount > int(^uint(0)>>1)-part.RowCount {
			return errors.New("manifest row_count overflow")
		}
		rowCount += part.RowCount
	}
	if rowCount != manifest.RowCount {
		return fmt.Errorf("row_count %d does not equal part row count %d", manifest.RowCount, rowCount)
	}
	return nil
}

func validatePartitionIdentity(partition map[string]string) error {
	if len(partition) != 3 {
		return errors.New("partition identity requires dataset, source, and key")
	}
	if partition["dataset"] == "" || partition["source"] == "" {
		return errors.New("partition dataset and source required")
	}
	for key, value := range partition {
		if key == "" || value == "" || strings.ContainsAny(key, "=\\/\x00") || strings.ContainsAny(value, "\\/\x00") {
			return fmt.Errorf("unsafe partition identity %q=%q", key, value)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validGitCommit(value string) bool {
	return value == UnknownGitCommit || validSHA1(value)
}

func validSHA1(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func contentPartFilename(hash string) string {
	return PartFilenamePrefix + hash + PartFilenameSuffix
}

func currentGitCommit() string {
	for _, key := range []string{"INVS_GIT_COMMIT", "GIT_COMMIT"} {
		if value := strings.TrimSpace(os.Getenv(key)); validGitCommit(value) {
			return value
		}
	}
	if output, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output(); err == nil {
		if value := strings.TrimSpace(string(output)); validGitCommit(value) {
			return value
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && validGitCommit(setting.Value) {
				return setting.Value
			}
		}
	}
	return UnknownGitCommit
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func syncFilePath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func syncDirectoryPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func writeManifest(path string, manifest Manifest, ops publicationOps) error {
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o640); err != nil {
		_ = tmp.Close()
		return err
	}
	if n, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	} else if n != len(contents) {
		_ = tmp.Close()
		return io.ErrShortWrite
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ops.syncFile(tmpName); err != nil {
		return err
	}
	if err := ops.rename(tmpName, path); err != nil {
		return err
	}
	removeTemp = false
	return ops.syncDir(filepath.Dir(path))
}

func partitionIdentityFromDir(root, dir string) (map[string]string, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	if len(segments) != 3 || segments[0] == "." || segments[0] == "" {
		return nil, fmt.Errorf("manifest directory %s is not a normalized partition", dir)
	}
	partition := map[string]string{"dataset": segments[0]}
	for _, segment := range segments[1:] {
		key, value, ok := strings.Cut(segment, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("manifest directory %s has invalid partition segment %q", dir, segment)
		}
		if _, exists := partition[key]; exists {
			return nil, fmt.Errorf("manifest directory %s repeats partition key %q", dir, key)
		}
		partition[key] = value
	}
	if err := validatePartitionIdentity(partition); err != nil {
		return nil, err
	}
	return partition, nil
}

func parquetFilesInDirectory(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != PartFilenameSuffix {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	return files, nil
}
