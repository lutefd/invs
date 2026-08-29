package metadata

import (
	"context"
	"errors"
	"fmt"
)

// SeedResearchThemeBundle publishes a reviewed, explicit theme fixture. Each
// child write is still routed through the same repository validation boundary
// used by interactive callers; the bundle is only a convenience for a bounded
// reference theme and is not a general provider import path.
func (r *Repository) SeedResearchThemeBundle(ctx context.Context, bundle ResearchThemeBundle) error {
	if err := requireResearchRepository(r); err != nil {
		return err
	}
	if bundle.SchemaVersion != ResearchSchemaVersion {
		return fmt.Errorf("unsupported research theme bundle schema_version %q", bundle.SchemaVersion)
	}
	if len(bundle.Themes) == 0 {
		return errors.New("research theme bundle must contain at least one theme")
	}
	for index, item := range bundle.Themes {
		theme, err := r.CreateResearchTheme(ctx, item.Theme)
		if err != nil {
			return fmt.Errorf("theme %d: %w", index, err)
		}
		item.Revision.ThemeID = theme.ID
		if _, err := r.AppendResearchThemeRevision(ctx, item.Revision); err != nil {
			return fmt.Errorf("theme %d revision: %w", index, err)
		}
	}
	for index, entity := range bundle.Entities {
		if _, err := r.CreateResearchEntity(ctx, entity); err != nil {
			return fmt.Errorf("entity %d: %w", index, err)
		}
	}
	for index, membership := range bundle.Memberships {
		if _, err := r.AppendResearchThemeMembership(ctx, membership); err != nil {
			return fmt.Errorf("membership %d: %w", index, err)
		}
	}
	for index, item := range bundle.Relationships {
		relationship, err := r.CreateResearchRelationship(ctx, item.Relationship)
		if err != nil {
			return fmt.Errorf("relationship %d: %w", index, err)
		}
		item.Revision.RelationshipID = relationship.ID
		if _, err := r.AppendResearchRelationshipRevision(ctx, item.Revision); err != nil {
			return fmt.Errorf("relationship %d revision: %w", index, err)
		}
	}
	for index, indicator := range bundle.Indicators {
		if _, err := r.AppendResearchThemeIndicator(ctx, indicator); err != nil {
			return fmt.Errorf("indicator %d: %w", index, err)
		}
	}
	for index, featureRef := range bundle.FeatureRefs {
		if _, err := r.AppendResearchThemeFeatureRef(ctx, featureRef); err != nil {
			return fmt.Errorf("feature reference %d: %w", index, err)
		}
	}
	for index, condition := range bundle.Conditions {
		if _, err := r.AppendResearchThemeCondition(ctx, condition); err != nil {
			return fmt.Errorf("condition %d: %w", index, err)
		}
	}
	return nil
}
