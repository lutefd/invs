package metadata

import (
	"encoding/json"
	"time"
)

const ResearchSchemaVersion = "1.0.0"

type ResearchEvidenceRef struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	SHA256  string `json:"sha256"`
	Locator string `json:"locator"`
}

type ResearchEntity struct {
	ID          string          `json:"id,omitempty"`
	EntityType  string          `json:"entity_type"`
	StableKey   string          `json:"stable_key"`
	DisplayName string          `json:"display_name"`
	IssuerID    string          `json:"issuer_id,omitempty"`
	SecurityID  string          `json:"security_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
}

type ResearchTheme struct {
	ID        string    `json:"id,omitempty"`
	StableKey string    `json:"stable_key"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ResearchThemeRevision struct {
	ThemeID       string                `json:"theme_id"`
	Revision      int                   `json:"revision,omitempty"`
	ParentThemeID string                `json:"parent_theme_id,omitempty"`
	Name          string                `json:"name"`
	KnowledgeKind string                `json:"knowledge_kind"`
	ReviewState   string                `json:"review_state"`
	Description   string                `json:"description"`
	EvidenceRefs  []ResearchEvidenceRef `json:"evidence_refs"`
	AuthorMethod  string                `json:"author_method"`
	RecordedAt    time.Time             `json:"recorded_at"`
	RecordHash    string                `json:"record_hash,omitempty"`
}

type ResearchThemeMembership struct {
	ThemeID       string                `json:"theme_id"`
	EntityID      string                `json:"entity_id"`
	Revision      int                   `json:"revision,omitempty"`
	Role          string                `json:"role"`
	KnowledgeKind string                `json:"knowledge_kind"`
	Confidence    float64               `json:"confidence"`
	EvidenceRefs  []ResearchEvidenceRef `json:"evidence_refs"`
	AuthorMethod  string                `json:"author_method"`
	ValidFrom     time.Time             `json:"valid_from"`
	ValidUntil    *time.Time            `json:"valid_until,omitempty"`
	RecordedAt    time.Time             `json:"recorded_at"`
	RevisionState string                `json:"revision_state"`
	RecordHash    string                `json:"record_hash,omitempty"`
}

type ResearchThemeIndicator struct {
	IndicatorID  string                `json:"indicator_id,omitempty"`
	ThemeID      string                `json:"theme_id"`
	IndicatorKey string                `json:"indicator_key"`
	DisplayName  string                `json:"display_name"`
	SourceRef    string                `json:"source_ref"`
	EvidenceRefs []ResearchEvidenceRef `json:"evidence_refs"`
	RecordedAt   time.Time             `json:"recorded_at"`
	RecordHash   string                `json:"record_hash,omitempty"`
}

type ResearchThemeFeatureRef struct {
	FeatureRefID      string                `json:"feature_ref_id,omitempty"`
	ThemeID           string                `json:"theme_id"`
	FeatureSet        string                `json:"feature_set"`
	FeatureSetVersion string                `json:"feature_set_version"`
	ArtifactRef       json.RawMessage       `json:"artifact_ref"`
	EvidenceRefs      []ResearchEvidenceRef `json:"evidence_refs"`
	RecordedAt        time.Time             `json:"recorded_at"`
	RecordHash        string                `json:"record_hash,omitempty"`
}

type ResearchThemeCondition struct {
	ConditionID   string                `json:"condition_id,omitempty"`
	ThemeID       string                `json:"theme_id"`
	ConditionType string                `json:"condition_type"`
	Condition     string                `json:"condition"`
	EvidenceRefs  []ResearchEvidenceRef `json:"evidence_refs"`
	RecordedAt    time.Time             `json:"recorded_at"`
	RecordHash    string                `json:"record_hash,omitempty"`
}

type ResearchThemeBundleTheme struct {
	Theme    ResearchTheme         `json:"theme"`
	Revision ResearchThemeRevision `json:"revision"`
}

type ResearchThemeBundleRelationship struct {
	Relationship ResearchRelationship         `json:"relationship"`
	Revision     ResearchRelationshipRevision `json:"revision"`
}

type ResearchThemeBundle struct {
	SchemaVersion string                            `json:"schema_version"`
	Themes        []ResearchThemeBundleTheme        `json:"themes"`
	Entities      []ResearchEntity                  `json:"entities"`
	Memberships   []ResearchThemeMembership         `json:"memberships"`
	Relationships []ResearchThemeBundleRelationship `json:"relationships"`
	Indicators    []ResearchThemeIndicator          `json:"indicators"`
	FeatureRefs   []ResearchThemeFeatureRef         `json:"feature_refs"`
	Conditions    []ResearchThemeCondition          `json:"conditions"`
}

type ResearchRelationship struct {
	ID        string    `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ResearchRelationshipRevision struct {
	RelationshipID   string                `json:"relationship_id"`
	Revision         int                   `json:"revision,omitempty"`
	FromEntityID     string                `json:"from_entity_id"`
	ToEntityID       string                `json:"to_entity_id"`
	RelationshipType string                `json:"relationship_type"`
	Direction        string                `json:"direction"`
	KnowledgeKind    string                `json:"knowledge_kind"`
	Confidence       float64               `json:"confidence"`
	EvidenceRefs     []ResearchEvidenceRef `json:"evidence_refs"`
	AuthorMethod     string                `json:"author_method"`
	ValidFrom        time.Time             `json:"valid_from"`
	ValidUntil       *time.Time            `json:"valid_until,omitempty"`
	RecordedAt       time.Time             `json:"recorded_at"`
	RevisionState    string                `json:"revision_state"`
	RecordHash       string                `json:"record_hash,omitempty"`
}

type ResearchDocument struct {
	ID                   string          `json:"id,omitempty"`
	Source               string          `json:"source"`
	SourceDocumentID     string          `json:"source_document_id"`
	MediaType            string          `json:"media_type"`
	SourceURI            string          `json:"source_uri"`
	PublishedAt          *time.Time      `json:"published_at,omitempty"`
	AvailableAt          time.Time       `json:"available_at"`
	RetrievedAt          time.Time       `json:"retrieved_at"`
	SupersedesDocumentID string          `json:"supersedes_document_id,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	CreatedAt            time.Time       `json:"created_at,omitempty"`
}

type ResearchDocumentEntityLink struct {
	DocumentID   string    `json:"document_id"`
	EntityID     string    `json:"entity_id"`
	RelationKind string    `json:"relation_kind"`
	RecordedAt   time.Time `json:"recorded_at,omitempty"`
}

type ResearchRawArtifact struct {
	ArtifactID  string    `json:"artifact_id,omitempty"`
	DocumentID  string    `json:"document_id"`
	Path        string    `json:"path"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

type ResearchDocumentTextArtifact struct {
	ArtifactID       string          `json:"artifact_id,omitempty"`
	DocumentID       string          `json:"document_id"`
	Status           string          `json:"status"`
	Path             string          `json:"path,omitempty"`
	InputSHA256      string          `json:"input_sha256"`
	OutputSHA256     string          `json:"output_sha256,omitempty"`
	Extractor        string          `json:"extractor"`
	ExtractorVersion string          `json:"extractor_version"`
	Config           json.RawMessage `json:"config,omitempty"`
	Locators         json.RawMessage `json:"locators,omitempty"`
	Errors           json.RawMessage `json:"errors,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
}

type ResearchEventProposal struct {
	ID        string    `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ResearchEventProposalRevision struct {
	ProposalID           string          `json:"proposal_id"`
	Revision             int             `json:"revision,omitempty"`
	DocumentID           string          `json:"document_id"`
	EventType            string          `json:"event_type"`
	Payload              json.RawMessage `json:"payload"`
	SourceSpans          json.RawMessage `json:"source_spans"`
	PromptVersion        string          `json:"prompt_version"`
	Model                string          `json:"model"`
	Parameters           json.RawMessage `json:"parameters,omitempty"`
	ExtractionVersion    string          `json:"extraction_version"`
	SourceDocumentSHA256 string          `json:"source_document_sha256"`
	Confidence           float64         `json:"confidence"`
	Status               string          `json:"status"`
	ProposedAt           time.Time       `json:"proposed_at"`
	ReviewerID           string          `json:"reviewer_id,omitempty"`
	ReviewMethod         string          `json:"review_method,omitempty"`
	ReviewedAt           *time.Time      `json:"reviewed_at,omitempty"`
	ReviewNote           string          `json:"review_note,omitempty"`
	RecordHash           string          `json:"record_hash,omitempty"`
}

type ResearchEvidencePack struct {
	ID            string    `json:"id"`
	DecisionAt    time.Time `json:"decision_at"`
	Path          string    `json:"path"`
	ContentSHA256 string    `json:"content_sha256"`
	CreatedAt     time.Time `json:"created_at"`
}

type ResearchHypothesis struct {
	ID        string    `json:"id,omitempty"`
	Title     string    `json:"title"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ResearchHypothesisRevision struct {
	HypothesisID       string          `json:"hypothesis_id"`
	Revision           int             `json:"revision,omitempty"`
	Thesis             string          `json:"thesis"`
	CausalModel        json.RawMessage `json:"causal_model"`
	Horizon            string          `json:"horizon"`
	Benchmark          string          `json:"benchmark"`
	Universe           json.RawMessage `json:"universe"`
	Invalidation       json.RawMessage `json:"invalidation_conditions"`
	DecisionAt         time.Time       `json:"decision_at"`
	EvidencePackID     string          `json:"evidence_pack_id"`
	EvidencePackSHA256 string          `json:"evidence_pack_sha256"`
	CreatedAt          time.Time       `json:"created_at,omitempty"`
	RecordHash         string          `json:"record_hash,omitempty"`
}

type ResearchHypothesisEvidence struct {
	EvidenceID         string          `json:"evidence_id,omitempty"`
	HypothesisID       string          `json:"hypothesis_id"`
	HypothesisRevision int             `json:"hypothesis_revision"`
	EvidenceRef        json.RawMessage `json:"evidence_ref"`
	Direction          string          `json:"direction"`
	Weight             float64         `json:"weight"`
	Note               string          `json:"note"`
	AvailableAt        time.Time       `json:"available_at"`
	CreatedAt          time.Time       `json:"created_at,omitempty"`
	RecordHash         string          `json:"record_hash,omitempty"`
}

type ResearchPrediction struct {
	ID                 string          `json:"id,omitempty"`
	HypothesisID       string          `json:"hypothesis_id"`
	HypothesisRevision int             `json:"hypothesis_revision"`
	AssetOrUniverse    json.RawMessage `json:"asset_or_universe"`
	ExpectedDirection  string          `json:"expected_direction"`
	ExpectedRange      json.RawMessage `json:"expected_range"`
	Horizon            string          `json:"horizon"`
	Confidence         float64         `json:"confidence"`
	CreatedAt          time.Time       `json:"created_at,omitempty"`
	FrozenAt           *time.Time      `json:"frozen_at,omitempty"`
	FrozenRevision     *int            `json:"frozen_revision,omitempty"`
	Status             string          `json:"status,omitempty"`
	RecordHash         string          `json:"record_hash,omitempty"`
}

type ResearchPredictionOutcome struct {
	OutcomeID         string          `json:"outcome_id,omitempty"`
	PredictionID      string          `json:"prediction_id"`
	MeasurementPolicy string          `json:"measurement_policy_version"`
	Status            string          `json:"status"`
	RealizedResult    json.RawMessage `json:"realized_result,omitempty"`
	BenchmarkResult   json.RawMessage `json:"benchmark_result,omitempty"`
	Drawdown          *float64        `json:"drawdown,omitempty"`
	MeasuredAt        time.Time       `json:"measured_at"`
	InputArtifactRefs json.RawMessage `json:"input_artifact_refs"`
	CreatedAt         time.Time       `json:"created_at,omitempty"`
	RecordHash        string          `json:"record_hash,omitempty"`
}
