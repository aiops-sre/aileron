-- Migration 007: add citations_json column to investigations table
-- Citations are LLM-generated evidence source references (HolmesGPT CitationRef pattern).
-- Stored as JSONB alongside the narrative so the API can return them without
-- a separate query.
ALTER TABLE investigations
    ADD COLUMN IF NOT EXISTS citations_json JSONB DEFAULT NULL;

COMMENT ON COLUMN investigations.citations_json IS
    'JSON array of CitationRef{source, evidence_type, description} from the OIE narrator. '
    'Populated when the LLM generates a narrative; nil when using the template fallback.';
