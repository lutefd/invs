package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrImmutableConflict = errors.New("raw object key already exists with different content")

type RawMetadata struct {
	Source      string            `json:"source"`
	ContentType string            `json:"content_type"`
	FetchedAt   time.Time         `json:"fetched_at"`
	SHA256      string            `json:"sha256"`
	Size        int64             `json:"size"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type RawStore interface {
	Put(ctx context.Context, key string, data io.Reader, metadata RawMetadata) (RawMetadata, error)
	Get(ctx context.Context, key string) (io.ReadCloser, RawMetadata, error)
}

// RawManifestVersion is the version of the run-scoped raw replay contract.
const RawManifestVersion = 1

// RawManifest is the immutable, logical index of raw objects successfully put
// during one ingestion run. Its serialized bytes, including the trailing
// newline, are the value hashed for ingestion_runs.raw_payload_manifest_hash.
type RawManifest struct {
	Version        int                `json:"version"`
	Source         string             `json:"source"`
	IngestionRunID string             `json:"ingestion_run_id"`
	Entries        []RawManifestEntry `json:"entries"`
}

// RawManifestEntry contains only RawStore keys. It deliberately does not
// expose a local filesystem path so the manifest remains replay-addressable if
// the RawStore implementation changes.
type RawManifestEntry struct {
	LogicalKey  string            `json:"logical_key"`
	ObjectKey   string            `json:"object_key"`
	SHA256      string            `json:"sha256"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type"`
	FetchedAt   time.Time         `json:"fetched_at"`
	Attributes  map[string]string `json:"attributes"`
}

// NewRawManifest creates an empty manifest. Empty runs therefore publish the
// same strict contract as non-empty runs.
func NewRawManifest(source, ingestionRunID string) RawManifest {
	return RawManifest{Version: RawManifestVersion, Source: source, IngestionRunID: ingestionRunID, Entries: []RawManifestEntry{}}
}

// AddRawManifestEntry records a successful RawStore.Put result. The metadata
// must be the metadata returned by Put, not the caller's requested metadata,
// because immutable stores may return the metadata of an existing object.
func (m *RawManifest) AddRawManifestEntry(logicalKey, objectKey string, metadata RawMetadata) error {
	entry := RawManifestEntry{
		LogicalKey:  logicalKey,
		ObjectKey:   objectKey,
		SHA256:      metadata.SHA256,
		Size:        metadata.Size,
		ContentType: metadata.ContentType,
		FetchedAt:   metadata.FetchedAt.UTC(),
		Attributes:  cloneAttributes(metadata.Attributes),
	}
	if err := validateRawManifestEntry(entry); err != nil {
		return err
	}
	if m.Entries == nil {
		m.Entries = []RawManifestEntry{}
	}
	m.Entries = append(m.Entries, entry)
	return nil
}

// RawManifestKey is the stable RawStore address of a run manifest.
func RawManifestKey(source, ingestionRunID string) (string, error) {
	if !validManifestSegment(source) || !validManifestSegment(ingestionRunID) {
		return "", errors.New("manifest source and ingestion run ID must be single safe path segments")
	}
	return path.Join("runs", source, ingestionRunID, "manifest.json"), nil
}

// MarshalRawManifest returns the canonical JSON bytes for a raw manifest. It
// sorts entries by all fields and relies on encoding/json's specified lexical
// map-key ordering for deterministic attributes, then appends one newline.
func MarshalRawManifest(manifest RawManifest) ([]byte, error) {
	canonical, err := canonicalRawManifest(manifest)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode raw manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// RawManifestHash returns the lowercase SHA-256 of the exact canonical JSON
// bytes, including its trailing newline.
func RawManifestHash(manifest RawManifest) (string, error) {
	b, err := MarshalRawManifest(manifest)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// PublishRawManifest immutably publishes a run manifest and returns the hash
// of its exact serialized bytes.
func PublishRawManifest(ctx context.Context, store RawStore, manifest RawManifest, fetchedAt time.Time) (string, string, error) {
	key, err := RawManifestKey(manifest.Source, manifest.IngestionRunID)
	if err != nil {
		return "", "", err
	}
	b, err := MarshalRawManifest(manifest)
	if err != nil {
		return "", "", err
	}
	stored, err := store.Put(ctx, key, bytes.NewReader(b), RawMetadata{
		Source:      manifest.Source,
		ContentType: "application/json",
		FetchedAt:   fetchedAt.UTC(),
		Attributes: map[string]string{
			"kind":             "raw-run-manifest",
			"ingestion_run_id": manifest.IngestionRunID,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("publish raw manifest %s: %w", key, err)
	}
	expectedHash := sha256.Sum256(b)
	hash := hex.EncodeToString(expectedHash[:])
	if stored.SHA256 != hash || stored.Size != int64(len(b)) {
		return "", "", fmt.Errorf("published raw manifest integrity mismatch for %s", key)
	}
	return hash, key, nil
}

// LoadAndVerifyRawManifest loads a manifest and verifies every listed object
// by reading it through RawStore.Get and checking its actual bytes and size.
// The returned hash is the hash of the exact manifest bytes loaded.
func LoadAndVerifyRawManifest(ctx context.Context, store RawStore, key string) (RawManifest, string, error) {
	r, metadata, err := store.Get(ctx, key)
	if err != nil {
		return RawManifest{}, "", fmt.Errorf("load raw manifest %s: %w", key, err)
	}
	b, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		return RawManifest{}, "", fmt.Errorf("read raw manifest %s: %w", key, readErr)
	}
	if closeErr != nil {
		return RawManifest{}, "", fmt.Errorf("close raw manifest %s: %w", key, closeErr)
	}
	h := sha256.Sum256(b)
	hash := hex.EncodeToString(h[:])
	if metadata.SHA256 != hash || metadata.Size != int64(len(b)) {
		return RawManifest{}, "", fmt.Errorf("raw manifest integrity mismatch for %s", key)
	}
	manifest, err := unmarshalRawManifest(b)
	if err != nil {
		return RawManifest{}, "", fmt.Errorf("decode raw manifest %s: %w", key, err)
	}
	expectedKey, err := RawManifestKey(manifest.Source, manifest.IngestionRunID)
	if err != nil || expectedKey != key {
		return RawManifest{}, "", fmt.Errorf("raw manifest address does not match %s", key)
	}
	for _, entry := range manifest.Entries {
		object, objectMetadata, err := store.Get(ctx, entry.ObjectKey)
		if err != nil {
			return RawManifest{}, "", fmt.Errorf("load raw manifest object %s: %w", entry.ObjectKey, err)
		}
		objectBytes, readErr := io.ReadAll(object)
		closeErr := object.Close()
		if readErr != nil {
			return RawManifest{}, "", fmt.Errorf("read raw manifest object %s: %w", entry.ObjectKey, readErr)
		}
		if closeErr != nil {
			return RawManifest{}, "", fmt.Errorf("close raw manifest object %s: %w", entry.ObjectKey, closeErr)
		}
		objectHash := sha256.Sum256(objectBytes)
		actualHash := hex.EncodeToString(objectHash[:])
		if actualHash != entry.SHA256 || int64(len(objectBytes)) != entry.Size || objectMetadata.SHA256 != entry.SHA256 || objectMetadata.Size != entry.Size {
			return RawManifest{}, "", fmt.Errorf("raw manifest object integrity mismatch for %s", entry.ObjectKey)
		}
	}
	return manifest, hash, nil
}

func canonicalRawManifest(manifest RawManifest) (RawManifest, error) {
	if manifest.Version != RawManifestVersion {
		return RawManifest{}, fmt.Errorf("unsupported raw manifest version %d", manifest.Version)
	}
	if !validManifestSegment(manifest.Source) || !validManifestSegment(manifest.IngestionRunID) {
		return RawManifest{}, errors.New("raw manifest source and ingestion run ID must be single safe path segments")
	}
	canonical := RawManifest{Version: manifest.Version, Source: manifest.Source, IngestionRunID: manifest.IngestionRunID, Entries: make([]RawManifestEntry, len(manifest.Entries))}
	for i, entry := range manifest.Entries {
		if err := validateRawManifestEntry(entry); err != nil {
			return RawManifest{}, fmt.Errorf("raw manifest entry %d: %w", i, err)
		}
		entry.Attributes = cloneAttributes(entry.Attributes)
		canonical.Entries[i] = entry
	}
	sort.Slice(canonical.Entries, func(i, j int) bool {
		left, right := canonical.Entries[i], canonical.Entries[j]
		for _, compare := range []func() int{
			func() int { return strings.Compare(left.LogicalKey, right.LogicalKey) },
			func() int { return strings.Compare(left.ObjectKey, right.ObjectKey) },
			func() int { return strings.Compare(left.SHA256, right.SHA256) },
			func() int { return compareInt64(left.Size, right.Size) },
			func() int { return strings.Compare(left.ContentType, right.ContentType) },
			func() int { return compareTime(left.FetchedAt, right.FetchedAt) },
		} {
			if result := compare(); result != 0 {
				return result < 0
			}
		}
		return canonicalAttributes(left.Attributes) < canonicalAttributes(right.Attributes)
	})
	if canonical.Entries == nil {
		canonical.Entries = []RawManifestEntry{}
	}
	return canonical, nil
}

func unmarshalRawManifest(b []byte) (RawManifest, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var manifest RawManifest
	if err := dec.Decode(&manifest); err != nil {
		return RawManifest{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return RawManifest{}, errors.New("trailing JSON in raw manifest")
		}
		return RawManifest{}, err
	}
	canonical, err := canonicalRawManifest(manifest)
	if err != nil {
		return RawManifest{}, err
	}
	canonicalBytes, err := MarshalRawManifest(canonical)
	if err != nil {
		return RawManifest{}, err
	}
	if !bytes.Equal(canonicalBytes, b) {
		return RawManifest{}, errors.New("raw manifest is not canonical JSON")
	}
	return canonical, nil
}

func validateRawManifestEntry(entry RawManifestEntry) error {
	if strings.TrimSpace(entry.LogicalKey) == "" {
		return errors.New("logical key is required")
	}
	if !validRawObjectKey(entry.ObjectKey) {
		return fmt.Errorf("invalid object key %q", entry.ObjectKey)
	}
	if len(entry.SHA256) != sha256.Size*2 || strings.ToLower(entry.SHA256) != entry.SHA256 {
		return fmt.Errorf("invalid SHA-256 %q", entry.SHA256)
	}
	if _, err := hex.DecodeString(entry.SHA256); err != nil {
		return fmt.Errorf("invalid SHA-256 %q: %w", entry.SHA256, err)
	}
	if entry.Size < 0 {
		return errors.New("size must be non-negative")
	}
	if strings.TrimSpace(entry.ContentType) == "" {
		return errors.New("content type is required")
	}
	if entry.Attributes == nil {
		return errors.New("attributes must be an object")
	}
	return nil
}

func validManifestSegment(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\') || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validRawObjectKey(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func cloneAttributes(attributes map[string]string) map[string]string {
	clone := make(map[string]string, len(attributes))
	for key, value := range attributes {
		clone[key] = value
	}
	return clone
}

func canonicalAttributes(attributes map[string]string) string {
	b, _ := json.Marshal(attributes)
	return string(b)
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareTime(a, b time.Time) int {
	if a.Before(b) {
		return -1
	}
	if a.After(b) {
		return 1
	}
	return 0
}

type FileRawStore struct{ root string }

func NewFileRawStore(root string) (*FileRawStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("raw store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve raw root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create raw root: %w", err)
	}
	return &FileRawStore{root: abs}, nil
}

// OpenFileRawStore opens an existing filesystem raw store without creating or
// changing any directories. Read-only operational commands use this boundary
// so a missing data root remains visible instead of being silently initialized.
func OpenFileRawStore(root string) (*FileRawStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("raw store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve raw root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat raw root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("raw root %s is not a directory", abs)
	}
	return &FileRawStore{root: abs}, nil
}

// Root returns the canonical filesystem root for diagnostics and read-only
// reconciliation. Callers must not mutate it through this accessor.
func (s *FileRawStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *FileRawStore) Put(ctx context.Context, key string, data io.Reader, metadata RawMetadata) (RawMetadata, error) {
	path, err := s.resolve(key)
	if err != nil {
		return RawMetadata{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return RawMetadata{}, fmt.Errorf("create object directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".raw-*")
	if err != nil {
		return RawMetadata{}, fmt.Errorf("create raw temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	n, copyErr := copyWithContext(ctx, io.MultiWriter(tmp, h), data)
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return RawMetadata{}, fmt.Errorf("write raw object: %w", copyErr)
	}
	metadata.SHA256 = hex.EncodeToString(h.Sum(nil))
	metadata.Size = n
	metadata.FetchedAt = metadata.FetchedAt.UTC()

	if current, statErr := readMetadata(path + ".metadata.json"); statErr == nil {
		if _, dataErr := os.Stat(path); errors.Is(dataErr, os.ErrNotExist) {
			return RawMetadata{}, fmt.Errorf("raw metadata exists without data for %s", key)
		} else if dataErr != nil {
			return RawMetadata{}, dataErr
		}
		if current.SHA256 != metadata.SHA256 {
			return RawMetadata{}, fmt.Errorf("%w: %s", ErrImmutableConflict, key)
		}
		return current, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RawMetadata{}, statErr
	}
	if _, statErr := os.Stat(path); statErr == nil {
		existingHash, existingSize, hashErr := hashFile(path)
		if hashErr != nil {
			return RawMetadata{}, hashErr
		}
		if existingHash != metadata.SHA256 || existingSize != metadata.Size {
			return RawMetadata{}, fmt.Errorf("%w: data exists without metadata for %s", ErrImmutableConflict, key)
		}
		if err := atomicJSON(path+".metadata.json", metadata); err != nil {
			return RawMetadata{}, err
		}
		return metadata, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RawMetadata{}, statErr
	}

	if err := os.Chmod(tmpName, 0o640); err != nil {
		return RawMetadata{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return RawMetadata{}, fmt.Errorf("publish raw object: %w", err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return RawMetadata{}, err
	}
	if err := atomicJSON(path+".metadata.json", metadata); err != nil {
		return RawMetadata{}, err
	}
	return metadata, nil
}

func (s *FileRawStore) Get(ctx context.Context, key string) (io.ReadCloser, RawMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, RawMetadata{}, err
	}
	path, err := s.resolve(key)
	if err != nil {
		return nil, RawMetadata{}, err
	}
	meta, err := readMetadata(path + ".metadata.json")
	if err != nil {
		return nil, RawMetadata{}, fmt.Errorf("read raw metadata: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, RawMetadata{}, fmt.Errorf("open raw object: %w", err)
	}
	hash, size, err := hashFile(path)
	if err != nil {
		f.Close()
		return nil, RawMetadata{}, err
	}
	if hash != meta.SHA256 || size != meta.Size {
		f.Close()
		return nil, RawMetadata{}, fmt.Errorf("raw object integrity mismatch for %s", key)
	}
	return f, meta, nil
}

func (s *FileRawStore) resolve(key string) (string, error) {
	if key == "" || filepath.IsAbs(key) || strings.ContainsRune(key, '\x00') {
		return "", errors.New("invalid raw object key")
	}
	clean := filepath.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("raw object key escapes store root")
	}
	return filepath.Join(s.root, clean), nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			wn, writeErr := dst.Write(buf[:n])
			total += int64(wn)
			if writeErr != nil {
				return total, writeErr
			}
			if wn != n {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(name, 0o640); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func readMetadata(path string) (RawMetadata, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return RawMetadata{}, err
	}
	var m RawMetadata
	if err := json.Unmarshal(b, &m); err != nil {
		return RawMetadata{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return m, nil
}
