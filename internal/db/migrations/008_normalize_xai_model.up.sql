-- Normalize stale xAI model IDs forward.
--
-- The xai catalog entry was changed from grok-4-fast-non-reasoning
-- (Grok 4 Fast) to grok-4-1-fast-non-reasoning (Grok 4.1 Fast). Both
-- are real xAI API model IDs and both support vision, but
-- ModelSupportsVision does an exact-match lookup against the
-- in-process catalog, so installs that saved the older ID hit a
-- false "this model is text-only" path.
--
-- Push them forward to today's default. Targeted UPDATE — anyone
-- who deliberately picked something else (custom model in a fork,
-- etc.) is left alone.
UPDATE llm_config
SET text_model = 'grok-4-1-fast-non-reasoning'
WHERE provider = 'xai'
  AND text_model = 'grok-4-fast-non-reasoning';
