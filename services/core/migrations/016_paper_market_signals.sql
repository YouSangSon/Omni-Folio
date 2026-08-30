CREATE TABLE paper_market_bar_observations (
    sequence INTEGER PRIMARY KEY,
    observation_id TEXT NOT NULL UNIQUE CHECK (length(observation_id) BETWEEN 1 AND 128 AND observation_id = trim(observation_id)),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-market-bar.v1'),
    source TEXT NOT NULL CHECK (source = 'paper_fixture'),
    source_observation_id TEXT NOT NULL CHECK (length(source_observation_id) BETWEEN 1 AND 128 AND source_observation_id = trim(source_observation_id)),
    input_data_sha256 TEXT NOT NULL CHECK (length(input_data_sha256) = 64 AND input_data_sha256 NOT GLOB '*[^0-9a-f]*'),
    symbol TEXT NOT NULL CHECK (length(symbol) = 6 AND symbol NOT GLOB '*[^0-9]*'),
    venue TEXT NOT NULL CHECK (venue = 'KRX'),
    currency TEXT NOT NULL CHECK (currency = 'KRW'),
    interval TEXT NOT NULL CHECK (interval = '1d'),
    timezone TEXT NOT NULL CHECK (timezone = 'Asia/Seoul'),
    price_adjustment TEXT NOT NULL CHECK (price_adjustment = 'unspecified'),
    open TEXT NOT NULL CHECK (
        length(open) BETWEEN 1 AND 64 AND open NOT GLOB '*[^0-9.]*'
        AND length(open) - length(replace(open, '.', '')) <= 1
        AND ((instr(open, '.') = 0 AND substr(open, 1, 1) GLOB '[1-9]')
          OR (instr(open, '.') > 1 AND (substr(open, 1, instr(open, '.') - 1) = '0' OR substr(open, 1, 1) GLOB '[1-9]') AND substr(open, -1) GLOB '[1-9]'))
    ),
    high TEXT NOT NULL CHECK (
        length(high) BETWEEN 1 AND 64 AND high NOT GLOB '*[^0-9.]*'
        AND length(high) - length(replace(high, '.', '')) <= 1
        AND ((instr(high, '.') = 0 AND substr(high, 1, 1) GLOB '[1-9]')
          OR (instr(high, '.') > 1 AND (substr(high, 1, instr(high, '.') - 1) = '0' OR substr(high, 1, 1) GLOB '[1-9]') AND substr(high, -1) GLOB '[1-9]'))
    ),
    low TEXT NOT NULL CHECK (
        length(low) BETWEEN 1 AND 64 AND low NOT GLOB '*[^0-9.]*'
        AND length(low) - length(replace(low, '.', '')) <= 1
        AND ((instr(low, '.') = 0 AND substr(low, 1, 1) GLOB '[1-9]')
          OR (instr(low, '.') > 1 AND (substr(low, 1, instr(low, '.') - 1) = '0' OR substr(low, 1, 1) GLOB '[1-9]') AND substr(low, -1) GLOB '[1-9]'))
    ),
    close TEXT NOT NULL CHECK (
        length(close) BETWEEN 1 AND 64 AND close NOT GLOB '*[^0-9.]*'
        AND length(close) - length(replace(close, '.', '')) <= 1
        AND ((instr(close, '.') = 0 AND substr(close, 1, 1) GLOB '[1-9]')
          OR (instr(close, '.') > 1 AND (substr(close, 1, instr(close, '.') - 1) = '0' OR substr(close, 1, 1) GLOB '[1-9]') AND substr(close, -1) GLOB '[1-9]'))
    ),
    volume TEXT NOT NULL CHECK (
        length(volume) BETWEEN 1 AND 64 AND volume NOT GLOB '*[^0-9.]*'
        AND length(volume) - length(replace(volume, '.', '')) <= 1
        AND (volume = '0' OR (instr(volume, '.') = 0 AND substr(volume, 1, 1) GLOB '[1-9]')
          OR (instr(volume, '.') > 1 AND (substr(volume, 1, instr(volume, '.') - 1) = '0' OR substr(volume, 1, 1) GLOB '[1-9]') AND substr(volume, -1) GLOB '[1-9]'))
    ),
    open_at TEXT NOT NULL CHECK (length(open_at) = 30 AND open_at GLOB '????-??-??T??:??:??.?????????Z' AND open_at NOT GLOB '*[^0-9TZ:.-]*'),
    close_at TEXT NOT NULL CHECK (length(close_at) = 30 AND close_at GLOB '????-??-??T??:??:??.?????????Z' AND close_at NOT GLOB '*[^0-9TZ:.-]*'),
    source_available_at TEXT NOT NULL CHECK (length(source_available_at) = 30 AND source_available_at GLOB '????-??-??T??:??:??.?????????Z' AND source_available_at NOT GLOB '*[^0-9TZ:.-]*'),
    fetched_at TEXT NOT NULL CHECK (length(fetched_at) = 30 AND fetched_at GLOB '????-??-??T??:??:??.?????????Z' AND fetched_at NOT GLOB '*[^0-9TZ:.-]*'),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (length(recorded_at) = 30 AND recorded_at GLOB '????-??-??T??:??:??.?????????Z' AND recorded_at NOT GLOB '*[^0-9TZ:.-]*'),
    UNIQUE (source, source_observation_id),
    UNIQUE (source, symbol, venue, interval, timezone, price_adjustment, open_at),
    CHECK (
        (instr(high || '.', '.') - 1 > instr(open || '.', '.') - 1
          OR (instr(high || '.', '.') - 1 = instr(open || '.', '.') - 1
            AND (substr(high, 1, instr(high || '.', '.') - 1) > substr(open, 1, instr(open || '.', '.') - 1) COLLATE BINARY
              OR (substr(high, 1, instr(high || '.', '.') - 1) = substr(open, 1, instr(open || '.', '.') - 1)
                AND replace(printf('%-64s', CASE WHEN instr(high, '.') = 0 THEN '' ELSE substr(high, instr(high, '.') + 1) END), ' ', '0')
                  >= replace(printf('%-64s', CASE WHEN instr(open, '.') = 0 THEN '' ELSE substr(open, instr(open, '.') + 1) END), ' ', '0') COLLATE BINARY))))
      AND (instr(high || '.', '.') - 1 > instr(close || '.', '.') - 1
          OR (instr(high || '.', '.') - 1 = instr(close || '.', '.') - 1
            AND (substr(high, 1, instr(high || '.', '.') - 1) > substr(close, 1, instr(close || '.', '.') - 1) COLLATE BINARY
              OR (substr(high, 1, instr(high || '.', '.') - 1) = substr(close, 1, instr(close || '.', '.') - 1)
                AND replace(printf('%-64s', CASE WHEN instr(high, '.') = 0 THEN '' ELSE substr(high, instr(high, '.') + 1) END), ' ', '0')
                  >= replace(printf('%-64s', CASE WHEN instr(close, '.') = 0 THEN '' ELSE substr(close, instr(close, '.') + 1) END), ' ', '0') COLLATE BINARY))))
      AND (instr(open || '.', '.') - 1 > instr(low || '.', '.') - 1
          OR (instr(open || '.', '.') - 1 = instr(low || '.', '.') - 1
            AND (substr(open, 1, instr(open || '.', '.') - 1) > substr(low, 1, instr(low || '.', '.') - 1) COLLATE BINARY
              OR (substr(open, 1, instr(open || '.', '.') - 1) = substr(low, 1, instr(low || '.', '.') - 1)
                AND replace(printf('%-64s', CASE WHEN instr(open, '.') = 0 THEN '' ELSE substr(open, instr(open, '.') + 1) END), ' ', '0')
                  >= replace(printf('%-64s', CASE WHEN instr(low, '.') = 0 THEN '' ELSE substr(low, instr(low, '.') + 1) END), ' ', '0') COLLATE BINARY))))
      AND (instr(close || '.', '.') - 1 > instr(low || '.', '.') - 1
          OR (instr(close || '.', '.') - 1 = instr(low || '.', '.') - 1
            AND (substr(close, 1, instr(close || '.', '.') - 1) > substr(low, 1, instr(low || '.', '.') - 1) COLLATE BINARY
              OR (substr(close, 1, instr(close || '.', '.') - 1) = substr(low, 1, instr(low || '.', '.') - 1)
                AND replace(printf('%-64s', CASE WHEN instr(close, '.') = 0 THEN '' ELSE substr(close, instr(close, '.') + 1) END), ' ', '0')
                  >= replace(printf('%-64s', CASE WHEN instr(low, '.') = 0 THEN '' ELSE substr(low, instr(low, '.') + 1) END), ' ', '0') COLLATE BINARY))))
    ),
    CHECK (open_at < close_at AND close_at <= source_available_at AND source_available_at <= fetched_at AND fetched_at <= recorded_at)
) STRICT;

CREATE TRIGGER paper_market_bar_observations_no_update
BEFORE UPDATE ON paper_market_bar_observations
BEGIN
    SELECT RAISE(ABORT, 'paper_market_bar_observations is insert-only');
END;

CREATE TRIGGER paper_market_bar_observations_no_delete
BEFORE DELETE ON paper_market_bar_observations
BEGIN
    SELECT RAISE(ABORT, 'paper_market_bar_observations is insert-only');
END;

CREATE TABLE paper_signal_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE CHECK (length(event_id) BETWEEN 1 AND 128 AND event_id = trim(event_id)),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-signal.v3'),
    account_ref TEXT NOT NULL CHECK (length(account_ref) BETWEEN 1 AND 128 AND account_ref = trim(account_ref) AND account_ref GLOB 'kiwoom_account_*'),
    paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id),
    strategy_result_sha256 TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256) CHECK (length(strategy_result_sha256) = 64 AND strategy_result_sha256 NOT GLOB '*[^0-9a-f]*'),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id) CHECK (length(strategy_selection_event_id) BETWEEN 1 AND 128 AND strategy_selection_event_id = trim(strategy_selection_event_id)),
    execution_policy_sha256 TEXT NOT NULL CHECK (length(execution_policy_sha256) = 64 AND execution_policy_sha256 NOT GLOB '*[^0-9a-f]*'),
    signal_id TEXT NOT NULL CHECK (length(signal_id) BETWEEN 1 AND 128 AND signal_id = trim(signal_id)),
    signal_bar_observation_id TEXT NOT NULL REFERENCES paper_market_bar_observations(observation_id),
    data_sha256 TEXT NOT NULL CHECK (length(data_sha256) = 64 AND data_sha256 NOT GLOB '*[^0-9a-f]*'),
    symbol TEXT NOT NULL CHECK (length(symbol) = 6 AND symbol NOT GLOB '*[^0-9]*'),
    target_quantity TEXT NOT NULL CHECK (target_quantity = '0' OR (length(target_quantity) BETWEEN 1 AND 32 AND substr(target_quantity, 1, 1) GLOB '[1-9]' AND target_quantity NOT GLOB '*[^0-9]*')),
    data_as_of TEXT NOT NULL CHECK (length(data_as_of) = 30 AND data_as_of GLOB '????-??-??T??:??:??.?????????Z' AND data_as_of NOT GLOB '*[^0-9TZ:.-]*'),
    generated_at TEXT NOT NULL CHECK (length(generated_at) = 30 AND generated_at GLOB '????-??-??T??:??:??.?????????Z' AND generated_at NOT GLOB '*[^0-9TZ:.-]*'),
    expires_at TEXT NOT NULL CHECK (length(expires_at) = 30 AND expires_at GLOB '????-??-??T??:??:??.?????????Z' AND expires_at NOT GLOB '*[^0-9TZ:.-]*'),
    market_observation_sequence_cutoff INTEGER NOT NULL CHECK (market_observation_sequence_cutoff > 0),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (length(recorded_at) = 30 AND recorded_at GLOB '????-??-??T??:??:??.?????????Z' AND recorded_at NOT GLOB '*[^0-9TZ:.-]*'),
    UNIQUE (account_ref, signal_id),
    CHECK (data_as_of <= generated_at AND generated_at < expires_at AND generated_at <= recorded_at AND recorded_at < expires_at)
) STRICT;

CREATE TRIGGER paper_signal_events_no_update
BEFORE UPDATE ON paper_signal_events
BEGIN
    SELECT RAISE(ABORT, 'paper_signal_events is insert-only');
END;

CREATE TRIGGER paper_signal_events_no_delete
BEFORE DELETE ON paper_signal_events
BEGIN
    SELECT RAISE(ABORT, 'paper_signal_events is insert-only');
END;

CREATE TRIGGER order_idempotency_legacy_paper_signal_guard
BEFORE INSERT ON order_idempotency
WHEN NEW.mode = 'paper'
  AND json_extract(NEW.intent_json, '$.signal_schema_version') IN ('paper-signal.v1', 'paper-signal.v2')
  AND EXISTS (SELECT 1 FROM paper_signal_events WHERE account_ref = NEW.account_ref)
BEGIN
    SELECT RAISE(ABORT, 'legacy paper order cannot follow a v3 signal');
END;

CREATE TRIGGER paper_signal_events_state_guard
BEFORE INSERT ON paper_signal_events
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM strategy_selection_events
        WHERE event_id = NEW.strategy_selection_event_id
          AND selected_result_sha256 = NEW.strategy_result_sha256
          AND event_id = (SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1)
    ) THEN RAISE(ABORT, 'paper signal strategy binding is not current') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM paper_accounting_sessions session
        JOIN strategy_research_evidence evidence ON evidence.result_sha256 = NEW.strategy_result_sha256
        WHERE session.session_id = NEW.paper_accounting_session_id
          AND session.account_ref = NEW.account_ref
          AND session.execution_policy_sha256 = NEW.execution_policy_sha256
          AND json_extract(session.execution_policy_json, '$.starting_cash') = json_extract(evidence.artifact_json, '$.execution.starting_cash')
          AND json_extract(session.execution_policy_json, '$.fee') = json_extract(evidence.artifact_json, '$.execution.fee')
          AND json_extract(session.execution_policy_json, '$.tax') = json_extract(evidence.artifact_json, '$.execution.tax')
          AND json_extract(session.execution_policy_json, '$.slippage_bps') = json_extract(evidence.artifact_json, '$.execution.slippage_bps')
          AND json_extract(session.execution_policy_json, '$.delay_bars') = json_extract(evidence.artifact_json, '$.execution.delay_bars')
          AND json_extract(session.execution_policy_json, '$.max_participation') = json_extract(evidence.artifact_json, '$.execution.max_participation')
          AND json_extract(session.execution_policy_json, '$.signal_price') = json_extract(evidence.artifact_json, '$.execution.signal_price')
          AND json_extract(session.execution_policy_json, '$.fill_price') = json_extract(evidence.artifact_json, '$.execution.fill_price')
    ) THEN RAISE(ABORT, 'paper signal accounting session binding is invalid') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM paper_market_bar_observations bar
        WHERE bar.observation_id = NEW.signal_bar_observation_id
          AND bar.source = 'paper_fixture' AND bar.symbol = NEW.symbol AND bar.venue = 'KRX'
          AND bar.currency = 'KRW' AND bar.interval = '1d' AND bar.timezone = 'Asia/Seoul'
          AND bar.price_adjustment = 'unspecified' AND bar.input_data_sha256 = NEW.data_sha256
          AND bar.close_at = NEW.data_as_of AND bar.source_available_at <= NEW.generated_at
          AND bar.sequence = (
              SELECT MAX(candidate.sequence) FROM paper_market_bar_observations candidate
              WHERE candidate.source = bar.source AND candidate.symbol = bar.symbol AND candidate.venue = bar.venue
                AND candidate.interval = bar.interval AND candidate.timezone = bar.timezone
                AND candidate.price_adjustment = bar.price_adjustment
                AND candidate.sequence <= NEW.market_observation_sequence_cutoff
          )
    ) THEN RAISE(ABORT, 'paper signal bar binding is invalid') END;
    SELECT CASE WHEN NEW.market_observation_sequence_cutoff != (SELECT MAX(sequence) FROM paper_market_bar_observations)
        THEN RAISE(ABORT, 'paper signal market cutoff is not transaction current') END;
    SELECT CASE WHEN EXISTS (
        SELECT 1 FROM order_idempotency
        WHERE mode = 'paper' AND account_ref = NEW.account_ref
          AND json_extract(intent_json, '$.signal_schema_version') IN ('paper-signal.v1', 'paper-signal.v2')
    ) THEN RAISE(ABORT, 'paper signal cannot follow a legacy paper order') END;
END;
