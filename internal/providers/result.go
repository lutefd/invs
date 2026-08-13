// Package providers contains contracts shared by the source-specific adapters.
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// RawResource is one response body that was successfully downloaded by a
// provider. It is returned even when parsing the body fails, so the collector
// can persist the exact evidence before publishing an error. The parser fields
// are optional source metadata; the collector owns the run-scoped logical key
// and raw-store object key.
type RawResource struct {
	Key            string
	Kind           string
	Year           int
	URL            string
	Bytes          []byte
	SHA256         string
	FetchedAt      time.Time
	ContentType    string
	ParserVersion  string
	ParserMetadata map[string]string
}

// ResourceResult is embedded by every provider result. Resources contains all
// bodies downloaded before a transport or parse error, in download order.
// Resources may therefore be non-empty when the provider also returns an error.
type ResourceResult struct {
	Resources []RawResource
}

// NewRawResource copies a downloaded body and computes its content hash. The
// copy prevents a caller from mutating evidence after the adapter returns it.
func NewRawResource(kind, key string, body []byte, fetchedAt time.Time, contentType string) RawResource {
	copyBody := append([]byte(nil), body...)
	return RawResource{
		Key:         key,
		Kind:        kind,
		Bytes:       copyBody,
		SHA256:      SHA256(copyBody),
		FetchedAt:   fetchedAt.UTC().Truncate(time.Microsecond),
		ContentType: contentType,
	}
}

// SHA256 returns the lowercase SHA-256 digest used by the raw evidence
// contract.
func SHA256(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
