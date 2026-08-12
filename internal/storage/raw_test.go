package storage

import (
	"context"
	"errors"
	"io"
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

func TestFileRawStoreRejectsTraversal(t *testing.T) {
	s, _ := NewFileRawStore(t.TempDir())
	if _, err := s.Put(context.Background(), "../escape", strings.NewReader("x"), RawMetadata{}); err == nil {
		t.Fatal("expected traversal error")
	}
}
