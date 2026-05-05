-- Drop the user-facing Base URL override.
--
-- Each provider now uses its hardcoded vendor URL exclusively. Routing
-- through Azure / OpenRouter / corporate proxies is no longer a supported
-- knob — keep the surface tight and the docs short. Tests still set the
-- BaseURL field on provider structs directly to point at httptest.Server.
ALTER TABLE llm_config DROP COLUMN base_url;
