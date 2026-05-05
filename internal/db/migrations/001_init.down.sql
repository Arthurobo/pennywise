-- Tear everything down in reverse-dependency order. categories/ledgers
-- before expenses isn't strictly required (we DROP them anyway), but
-- telegram_config before ledgers matters because telegram_config
-- references ledgers via active_ledger_id.
DROP TABLE IF EXISTS llm_call_log;
DROP TABLE IF EXISTS telegram_config;
DROP TABLE IF EXISTS llm_config;
DROP TABLE IF EXISTS expenses;
DROP TABLE IF EXISTS ledgers;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS app_state;
DROP TABLE IF EXISTS owner;
