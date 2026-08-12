package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileRawStoreIdempotentAndImmutable(t *testing.T) {
	s, err := NewFileRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta := RawMetadata{Source: "test", FetchedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))}
	first, err := s.Put(context.Background(), "source/2024/object.json", strings.NewReader("payload"), meta)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put(context.Background(), "source/2024/object.json", strings.NewReader("payload"), meta)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || first.Size != 7 {
		t.Fatalf("unexpected metadata: %+v %+v", first, second)
	}
	if first.FetchedAt.Location() != time.UTC {
		t.Fatal("timestamp was not normalized to UTC")
	}
	_, err = s.Put(context.Background(), "source/2024/object.json", strings.NewReader("changed"), meta)
	if !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("expected immutable conflict, got %v", err)
	}
	r, gotMeta, err := s.Get(context.Background(), "source/2024/object.json")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "payload" || gotMeta.SHA256 != first.SHA256 {
		t.Fatal("stored object changed")
	}
}

func TestFileRawStoreRepairsIdenticalDataOrphan(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileRawStore(root)
	path := filepath.Join(root, "x")
	if err := os.WriteFile(path, []byte("same"), 0o640); err != nil {
		t.Fatal(err)
	}
	m, err := s.Put(context.Background(), "x", strings.NewReader("same"), RawMetadata{Source: "x", FetchedAt: time.Now()})
	if err != nil || m.Size != 4 {
		t.Fatalf("m=%+v err=%v", m, err)
	}
	if _, err := os.Stat(path + ".metadata.json"); err != nil {
		t.Fatal(err)
	}
}
func TestFileRawStoreRejectsDifferentDataOrphan(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileRawStore(root)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := s.Put(context.Background(), "x", strings.NewReader("new"), RawMetadata{})
	if !errors.Is(err, ErrImmutableConflict) {
		t.Fatal(err)
	}
}
func TestFileRawStoreRejectsMetadataOnlyAndMismatch(t *testing.T) {
	root := t.TempDir()
	s, _ := NewFileRawStore(root)
	if err := os.WriteFile(filepath.Join(root, "x.metadata.json"), []byte(`{"sha256":"00","size":1}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), "x", strings.NewReader("x"), RawMetadata{}); err == nil {
		t.Fatal("expected metadata orphan error")
	}
	_ = os.Remove(filepath.Join(root, "x.metadata.json"))
	if _, err := s.Put(context.Background(), "x", strings.NewReader("good"), RawMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("evil"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(context.Background(), "x"); err == nil {
		t.Fatal("expected integrity error")
	}
}

func TestFileRawStoreRejectsTraversal(t *testing.T) {
	s, _ := NewFileRawStore(t.TempDir())
	if _, err := s.Put(context.Background(), "../escape", strings.NewReader("x"), RawMetadata{}); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestRawManifestSerializationIsDeterministic(t *testing.T) {
	first := NewRawManifest("yahoo", "run-1")
	second := NewRawManifest("yahoo", "run-1")
	entryA := RawManifestEntry{LogicalKey: "price/AAPL", ObjectKey: "objects/a", SHA256: strings.Repeat("a", 64), Size: 3, ContentType: "application/json", FetchedAt: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC), Attributes: map[string]string{"vendor_symbol": "AAPL", "security_id": "security-a"}}
	entryB := RawManifestEntry{LogicalKey: "price/MSFT", ObjectKey: "objects/b", SHA256: strings.Repeat("b", 64), Size: 4, ContentType: "application/json", FetchedAt: time.Date(2026, 8, 12, 1, 2, 4, 0, time.UTC), Attributes: map[string]string{"security_id": "security-b", "vendor_symbol": "MSFT"}}
	first.Entries = []RawManifestEntry{entryB, entryA}
	second.Entries = []RawManifestEntry{entryA, entryB}

	firstBytes, err := MarshalRawManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalRawManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("manifest serialization differs for input order:\n%s\n%s", firstBytes, secondBytes)
	}
	if !strings.HasSuffix(string(firstBytes), "}\n") {
		t.Fatal("manifest does not use the trailing newline convention")
	}
}

func TestRawManifestPublishLoadAndTamperVerification(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileRawStore(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("raw payload")
	stored, err := store.Put(context.Background(), "objects/payload.json", strings.NewReader(string(payload)), RawMetadata{Source: "test", ContentType: "application/json", FetchedAt: time.Now().UTC(), Attributes: map[string]string{"request": "one"}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := NewRawManifest("test", "run-1")
	if err := manifest.AddRawManifestEntry("request/one", "objects/payload.json", stored); err != nil {
		t.Fatal(err)
	}
	hash, key, err := PublishRawManifest(context.Background(), store, manifest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedHash, err := LoadAndVerifyRawManifest(context.Background(), store, key)
	if err != nil {
		t.Fatal(err)
	}
	if loadedHash != hash || len(loaded.Entries) != 1 || loaded.Entries[0].ObjectKey != "objects/payload.json" {
		t.Fatalf("loaded manifest = %+v hash=%s, want hash %s", loaded, loadedHash, hash)
	}

	changed := NewRawManifest("test", "run-1")
	if err := changed.AddRawManifestEntry("request/two", "objects/payload.json", stored); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PublishRawManifest(context.Background(), store, changed, time.Now().UTC()); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("changed manifest publish error = %v, want immutable conflict", err)
	}

	if err := os.WriteFile(filepath.Join(root, "objects/payload.json"), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadAndVerifyRawManifest(context.Background(), store, key); err == nil {
		t.Fatal("tampered listed object verified successfully")
	}
}

func TestEmptyRawManifestIsValid(t *testing.T) {
	store, err := NewFileRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := NewRawManifest("fred", "run-empty")
	_, key, err := PublishRawManifest(context.Background(), store, manifest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadAndVerifyRawManifest(context.Background(), store, key)
	if err != nil || len(loaded.Entries) != 0 {
		t.Fatalf("empty manifest load = %+v err=%v", loaded, err)
	}
}
