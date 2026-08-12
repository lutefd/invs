package normalize

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/luisdourado/invs/internal/model"
	"github.com/parquet-go/parquet-go"
)

func filingForTest(sourceDocumentID, hash, runID string, ingested time.Time) model.Filing {
	periodEnd := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	return model.Filing{
		ID:               "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201",
		Source:           "cvm",
		IssuerID:         issuerID,
		SourceDocumentID: sourceDocumentID,
		DocumentURL:      "https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/IPE/DADOS/ipe_cia_aberta_2026.zip",
		AccessionNumber:  "12345",
		FormType:         "cvm_ipe",
		Category:         "Categoria exata; ação",
		DocumentType:     "Tipo exato",
		Species:          "Espécie exata",
		Subject:          "Assunto exato",
		PresentationType: "Tipo_Apresentacao exato",
		PrimaryDocument:  "not-downloaded",
		FilingDate:       time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        &periodEnd,
		Temporal: model.Temporal{
			ObservedAt:         periodEnd,
			ObservedPrecision:  model.PrecisionDate,
			PublishedPrecision: model.PrecisionUnknown,
			AvailableAt:        ingested,
			IngestedAt:         ingested,
		},
		RawPayloadHash: hash,
		Provenance: model.Provenance{
			DataSourceID:      sourceID,
			IngestionRunID:    runID,
			RawPayloadHash:    hash,
			RawRecordLocator:  "zip=2026/member=ipe.csv/row=7",
			IngestedAt:        ingested,
			NormalizerVersion: model.NormalizerVersion,
		},
	}
}

func TestFilingPhysicalSchemaContainsCanonicalColumns(t *testing.T) {
	columns := columnsOf[FilingRow]()
	for _, name := range []string{
		"schema_version", "id", "source", "issuer_id", "source_document_id", "document_url",
		"accession_number", "form_type", "category", "document_type", "species", "subject",
		"presentation_type", "amends_source_document_id", "filing_date", "period_end", "has_period_end", "observed_at",
		"has_observed_at", "observed_precision", "published_at", "has_published_at",
		"published_precision", "available_at", "effective_at", "has_effective_at", "ingested_at",
		"raw_payload_hash", "data_source_id", "ingestion_run_id", "raw_record_locator",
		"normalizer_version",
	} {
		if !columns[name] {
			t.Errorf("filing schema missing %s", name)
		}
	}
}

func TestWriteFilingsPreservesExactMetadataAndVersionedIdentity(t *testing.T) {
	root := t.TempDir()
	w, err := NewWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	ingested := time.Date(2026, 8, 12, 15, 4, 5, 123456000, time.UTC)
	first := filingForTest("cvm-ipe:1023:12345:v1", rawHash, runID, ingested)
	second := first
	second.ID = "2c4e99f6-66c9-4eb6-b7cf-3f88f0a88312"
	second.SourceDocumentID = "cvm-ipe:1023:12345:v2"
	second.Category = "Categoria exata; versão 2"

	path, n, err := w.WriteFilings(issuerID, []model.Filing{first, second, first})
	if err != nil || n != 2 {
		t.Fatalf("path=%q n=%d err=%v", path, n, err)
	}
	wantPath := filepath.Join(root, "filings", "source=cvm", "issuer_id="+issuerID, ManifestFilename)
	if path != wantPath {
		t.Fatalf("path=%q want %q", path, wantPath)
	}
	rows := rowsFromManifest[FilingRow](t, path)
	if len(rows) != 2 {
		t.Fatalf("rows=%d want 2", len(rows))
	}
	for _, row := range rows {
		if row.DocumentURL != first.DocumentURL || row.FormType != "cvm_ipe" || row.Species != "Espécie exata" {
			t.Fatalf("source strings changed: %+v", row)
		}
		if row.AvailableAt != ingested.UnixMicro() || row.IngestedAt != ingested.UnixMicro() {
			t.Fatalf("availability was not explicit receipt time: %+v", row)
		}
		if row.HasPublishedAt || row.PublishedPrecision != string(model.PrecisionUnknown) {
			t.Fatalf("unknown publication was invented: %+v", row)
		}
		if !row.HasObservedAt || row.ObservedPrecision != string(model.PrecisionDate) {
			t.Fatalf("observed date precision was lost: %+v", row)
		}
	}
	if err := w.ValidateExisting(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFilingsRetryIsIdempotentAndKeepsFirstReceipt(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	firstAt := time.Date(2026, 8, 12, 15, 4, 5, 123456000, time.UTC)
	first := filingForTest("cvm-ipe:1023:12345:v1", rawHash, runID, firstAt)
	path, n, err := w.WriteFilings(issuerID, []model.Filing{first})
	if err != nil || n != 1 {
		t.Fatalf("initial n=%d err=%v", n, err)
	}
	retryAt := firstAt.Add(time.Hour)
	retry := filingForTest("cvm-ipe:1023:12345:v1", rawHash, "b2468ace-1357-4bdf-9024-6e2f59b9527a", retryAt)
	gotPath, n, err := w.WriteFilings(issuerID, []model.Filing{retry})
	if err != nil || n != 0 || gotPath != path {
		t.Fatalf("retry path=%q n=%d err=%v", gotPath, n, err)
	}
	rows := rowsFromManifest[FilingRow](t, path)
	if len(rows) != 1 || rows[0].AvailableAt != firstAt.UnixMicro() || rows[0].IngestedAt != firstAt.UnixMicro() {
		t.Fatalf("retry changed receipt metadata: %+v", rows)
	}
}

func TestWriteFilingsChangedRawHashConflicts(t *testing.T) {
	w, _ := NewWriter(t.TempDir())
	at := time.Date(2026, 8, 12, 15, 4, 5, 123456000, time.UTC)
	first := filingForTest("cvm-ipe:1023:12345:v1", rawHash, runID, at)
	if _, _, err := w.WriteFilings(issuerID, []model.Filing{first}); err != nil {
		t.Fatal(err)
	}
	changed := filingForTest("cvm-ipe:1023:12345:v1", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", runID, at)
	if _, _, err := w.WriteFilings(issuerID, []model.Filing{changed}); !errors.Is(err, ErrNaturalKeyConflict) {
		t.Fatalf("err=%v want natural-key conflict", err)
	}
}

func TestFilingRequiresExplicitAvailability(t *testing.T) {
	at := time.Date(2026, 8, 12, 15, 4, 5, 123456000, time.UTC)
	filing := filingForTest("cvm-ipe:1023:12345:v1", rawHash, runID, at)
	filing.Temporal.AvailableAt = time.Time{}
	if _, err := filingRow(filing); err == nil {
		t.Fatal("accepted filing without explicit available_at")
	}
}

func TestFilingParquetTimestampPhysicalTypes(t *testing.T) {
	for _, name := range []string{"observed_at", "published_at", "available_at", "effective_at", "ingested_at"} {
		column, ok := parquet.SchemaOf(new(FilingRow)).Lookup(name)
		if !ok || column.Node.Type().Kind() != parquet.Int64 {
			t.Fatalf("%s schema=%v want INT64 timestamp", name, column.Node)
		}
	}
}
