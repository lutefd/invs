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
