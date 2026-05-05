-- No-op revert. Both IDs are valid xAI models and both support vision;
-- there's no functional reason to walk the rename backwards. We leave
-- normalized rows alone so a down → up cycle doesn't churn user data.
SELECT 1;
