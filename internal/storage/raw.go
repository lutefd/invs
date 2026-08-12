package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		if current.SHA256 != metadata.SHA256 {
			return RawMetadata{}, fmt.Errorf("%w: %s", ErrImmutableConflict, key)
		}
		return current, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RawMetadata{}, statErr
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return RawMetadata{}, fmt.Errorf("%w: data exists without metadata for %s", ErrImmutableConflict, key)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RawMetadata{}, statErr
	}

	if err := os.Chmod(tmpName, 0o640); err != nil {
		return RawMetadata{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return RawMetadata{}, fmt.Errorf("publish raw object: %w", err)
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
	return os.Rename(name, path)
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
