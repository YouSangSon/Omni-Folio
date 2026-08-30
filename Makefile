SHELL := /bin/sh

ROOT := $(CURDIR)
GO ?= go
GOFMT ?= gofmt
FLUTTER ?= flutter
DART ?= dart
PYTHON ?= python3

DB_PATH ?= $(ROOT)/data/omni-folio.db
CORE_ADDR ?= 127.0.0.1:8080
API_URL ?= http://$(CORE_ADDR)
FLUTTER_DEVICE ?= chrome
FLUTTER_WEB_HOST ?= localhost
FLUTTER_WEB_PORT ?= 8081
WEB_ORIGIN ?= http://$(FLUTTER_WEB_HOST):$(FLUTTER_WEB_PORT)
RESEARCH_PYTHONPATH ?= $(ROOT)/services/research
MARKET_FIXTURE ?= $(ROOT)/contracts/fixtures/market-bars.csv

.PHONY: bootstrap format format-check lint test contract-check check clean clean-test-resources run-core run-client run-research run-improvement smoke

bootstrap:
	mkdir -p "$(ROOT)/data"
	cd services/core && "$(GO)" mod download
	cd apps/client && "$(FLUTTER)" pub get
	"$(PYTHON)" -m compileall -q services/research/omni_research

format:
	cd services/core && "$(GO)" fmt ./...
	"$(DART)" format apps/client/lib apps/client/test apps/client/integration_test apps/client/test_driver

format-check:
	test -z "$$($(GOFMT) -l services/core/*.go)"
	cd apps/client && "$(DART)" format --output=none --set-exit-if-changed lib test integration_test test_driver

lint:
	cd services/core && "$(GO)" vet ./...
	cd apps/client && "$(FLUTTER)" analyze
	"$(PYTHON)" -m compileall -q services/research

test:
	cd services/core && "$(GO)" test ./...
	cd apps/client && "$(FLUTTER)" test
	cd services/research && "$(PYTHON)" -m unittest discover -s tests -v

contract-check:
	"$(PYTHON)" -c 'import json, pathlib; files=sorted(pathlib.Path("contracts").rglob("*.json")); assert files, "no JSON contract files found"; [json.loads(path.read_text(encoding="utf-8")) for path in files]; print(f"validated {len(files)} JSON contract files")'

check:
	+@cleanup() { \
		cleanup_status=0; \
		trap - EXIT INT TERM; \
		$(MAKE) clean-test-resources || cleanup_status=$$?; \
		if test "$$status" -ne 0; then exit "$$status"; fi; \
		exit "$$cleanup_status"; \
	}; \
	trap 'status=130; cleanup' INT; \
	trap 'status=143; cleanup' TERM; \
	trap 'status=$$?; cleanup' EXIT; \
	$(MAKE) format-check lint test contract-check

clean-test-resources:
	@set -eu; \
	for artifact_path in \
		"$(ROOT)/apps/client/build" \
		"$(ROOT)/apps/client/coverage" \
		"$(ROOT)/services/core/core"; do \
		if test -e "$$artifact_path"; then find "$$artifact_path" -depth -delete; fi; \
	done; \
	find "$(ROOT)/services/research" -type f \( -name '*.pyc' -o -name '*.pyo' \) -delete; \
	find "$(ROOT)/services/research" -depth -type d -name __pycache__ -empty -delete

clean: clean-test-resources
	@set -eu; \
	for artifact_path in "$(ROOT)/.playwright-cli" "$(ROOT)/output"; do \
		if test -e "$$artifact_path"; then find "$$artifact_path" -depth -delete; fi; \
	done
	cd apps/client && "$(FLUTTER)" clean

run-core:
	mkdir -p "$(dir $(DB_PATH))"
	cd services/core && "$(GO)" run . migrate -db "$(DB_PATH)"
	cd services/core && "$(GO)" run . serve -db "$(DB_PATH)" -addr "$(CORE_ADDR)" -allow-origin "$(WEB_ORIGIN)" -market-fixture "$(MARKET_FIXTURE)"

run-client:
	cd apps/client && "$(FLUTTER)" run -d "$(FLUTTER_DEVICE)" --web-hostname "$(FLUTTER_WEB_HOST)" --web-port "$(FLUTTER_WEB_PORT)" --dart-define=OMNI_API_URL="$(API_URL)"

run-research:
	@PYTHONPATH="$(RESEARCH_PYTHONPATH)" "$(PYTHON)" -m omni_research \
		--bars "$(ROOT)/contracts/fixtures/market-bars.csv" \
		--request "$(ROOT)/contracts/fixtures/backtest-request.json" \
		--run-id run_buy_hold_fixture_001 \
		--dataset-id dataset_market_bars_fixture \
		--started-at 2026-08-23T06:00:00Z \
		--finished-at 2026-08-23T06:00:00.010Z

run-improvement:
	@PYTHONPATH="$(RESEARCH_PYTHONPATH)" "$(PYTHON)" -m omni_research.improve_cli \
		--bars "$(ROOT)/contracts/fixtures/strategy-market-bars.csv" \
		--config "$(ROOT)/contracts/fixtures/strategy-improvement-config.json"

smoke:
	@set -eu; \
	smoke_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	pid=; \
	trap 'status=$$?; if test -n "$$pid"; then kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; fi; rm -rf "$$smoke_dir"; exit "$$status"' EXIT INT TERM; \
	db="$$smoke_dir/omni-folio.db"; \
	bin="$$smoke_dir/omni-core"; \
	log="$$smoke_dir/core.log"; \
	(cd services/core && "$(GO)" build -o "$$bin" .); \
	"$$bin" migrate -db "$$db"; \
	"$$bin" serve -db "$$db" -addr 127.0.0.1:18080 -market-fixture "$(MARKET_FIXTURE)" >"$$log" 2>&1 & \
	pid=$$!; \
	ready=0; \
	for _ in $$(seq 1 30); do \
		if curl --fail --silent http://127.0.0.1:18080/healthz >/dev/null; then ready=1; break; fi; \
		sleep 1; \
	done; \
	test "$$ready" -eq 1 || { cat "$$log" >&2; exit 1; }; \
	curl --fail --silent http://127.0.0.1:18080/readyz >/dev/null; \
	status_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/status)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["service"] == "omni-folio" and d["ledger_revision"] == "rev_0000000000" and d["live_enabled"] is False' "$$status_json"; \
	preview_json="$$(curl --fail --silent -X POST -H 'Content-Type: text/csv' --data-binary @contracts/fixtures/golden-import.csv http://127.0.0.1:18080/v1/imports/preview)"; \
	preview_id="$$(printf '%s' "$$preview_json" | "$(PYTHON)" -c 'import json,sys; print(json.load(sys.stdin)["preview_id"])')"; \
	apply_json="$$(curl --fail --silent -X POST -H 'Content-Type: application/json' --data "{\"preview_id\":\"$$preview_id\",\"idempotency_key\":\"smoke-apply-001\"}" http://127.0.0.1:18080/v1/imports/apply)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["applied_rows"] == 3 and d["ledger_revision_after"] == "rev_0000000003"' "$$apply_json"; \
	snapshot_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/portfolio/snapshot)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["ledger_revision"] == "rev_0000000003" and d["live_enabled"] is False and d["cash"][0]["amount"] == "778"' "$$snapshot_json"; \
	activity_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/ledger/activities)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["source"] == "local_ledger" and d["broker_freshness"] == "unverified" and d["ledger_revision"] == "rev_0000000003" and [row["type"] for row in d["events"]] == ["SELL", "BUY", "DEPOSIT"] and d["next_cursor"] is None; assert not ({"event_id", "source_event_id", "account_id", "instrument_id", "receipt_id", "corrects_source_event_id", "sequence"} & set().union(*(row.keys() for row in d["events"])))' "$$activity_json"; \
	market_json="$$(curl --fail --silent 'http://127.0.0.1:18080/v1/market-data/candles?symbol=AAPL&interval=1d')"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["symbol"] == "AAPL" and d["price_adjustment"] == "unspecified" and d["source"] == "local_fixture" and d["sample"] is True and d["state"] == "stale" and d["issues"][0]["code"] == "sample_data" and len(d["bars"]) == 6 and d["bars"][0]["open"] == "10" and d["bars"][-1]["close"] == "16"' "$$market_json"; \
	printf '%s\n' 'smoke: health, status, preview, apply, snapshot, activity, market data OK'
