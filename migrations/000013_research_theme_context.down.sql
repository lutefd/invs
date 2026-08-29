BEGIN;

DROP TRIGGER IF EXISTS research_theme_condition_immutable ON research_theme_conditions;
DROP TRIGGER IF EXISTS research_theme_feature_ref_immutable ON research_theme_feature_refs;
DROP TRIGGER IF EXISTS research_theme_indicator_immutable ON research_theme_indicators;

DROP TABLE IF EXISTS research_theme_conditions;
DROP TABLE IF EXISTS research_theme_feature_refs;
DROP TABLE IF EXISTS research_theme_indicators;

COMMIT;
