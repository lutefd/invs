BEGIN;

DROP VIEW IF EXISTS backtest_run_status;
DROP TABLE IF EXISTS backtest_run_events;
DROP TABLE IF EXISTS backtest_runs;
DROP TABLE IF EXISTS backtest_experiment_inputs;
DROP TABLE IF EXISTS backtest_experiments;
DROP FUNCTION IF EXISTS backtest_run_event_order_guard();

COMMIT;
