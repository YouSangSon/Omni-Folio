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
SEED_DEMO_CSV ?= $(ROOT)/contracts/fixtures/golden-import.csv

.PHONY: bootstrap format format-check lint test test-body test-resource-cleanup contract-check check clean clean-test-resources run-core run-client seed-demo run-research run-improvement smoke

bootstrap:
	mkdir -p "$(ROOT)/data"
	cd services/core && "$(GO)" mod download
	cd apps/client && "$(FLUTTER)" pub get

format:
	cd services/core && "$(GO)" fmt ./...
	"$(DART)" format apps/client/lib apps/client/test apps/client/integration_test apps/client/test_driver

format-check:
	test -z "$$($(GOFMT) -l services/core/*.go)"
	cd apps/client && "$(DART)" format --output=none --set-exit-if-changed lib test integration_test test_driver

lint:
	cd services/core && "$(GO)" vet ./...
	cd apps/client && "$(FLUTTER)" analyze
	"$(PYTHON)" -c 'import pathlib; files=sorted(pathlib.Path("services/research").rglob("*.py")); assert files, "no Python source files found"; [compile(path.read_bytes(), str(path), "exec") for path in files]'

test:
	+@test_root=; \
	status=0; \
	child_pid=; \
	cleanup() { \
		cleanup_status=0; \
		trap - EXIT INT TERM; \
		if test -n "$$child_pid"; then /bin/kill -TERM -- "-$$child_pid" 2>/dev/null || true; wait "$$child_pid" 2>/dev/null || true; fi; \
		if test -n "$$test_root"; then \
			$(MAKE) --no-print-directory clean-test-resources TEST_SESSION_ROOT="$$test_root" || cleanup_status=$$?; \
		else \
			$(MAKE) --no-print-directory clean-test-resources || cleanup_status=$$?; \
		fi; \
		if test "$$status" -ne 0; then exit "$$status"; fi; \
		exit "$$cleanup_status"; \
	}; \
	trap 'status=130; cleanup' INT; \
	trap 'status=143; cleanup' TERM; \
	trap 'status=$$?; cleanup' EXIT; \
	test_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-test.XXXXXX")"; \
	printf '%s\n' "$$$$" >"$$test_root/.owner-pid"; \
	ps -p "$$$$" -o command= >"$$test_root/.owner-command"; \
	ps -p "$$$$" -o lstart= | awk '{$$1=$$1; print}' >"$$test_root/.owner-start"; \
	: >"$$test_root/.child-starting"; \
	TMPDIR="$$test_root" "$(PYTHON)" -c 'import os,pathlib,subprocess,sys; os.setsid(); root=pathlib.Path(sys.argv[1]); root.joinpath(".child-pgid").write_text(f"{os.getpid()}\n"); started=" ".join(subprocess.check_output(["ps","-p",str(os.getpid()),"-o","lstart="], text=True).split()); root.joinpath(".child-start").write_text(started+"\n"); root.joinpath(".child-starting").unlink(missing_ok=True); os.execvp(sys.argv[2], sys.argv[2:])' "$$test_root" "$(MAKE)" --no-print-directory test-body & \
	child_pid=$$!; \
	child_status=0; \
	wait "$$child_pid" || child_status=$$?; \
	child_pid=; \
	exit "$$child_status"

test-body:
	cd services/core && "$(GO)" test ./...
	cd apps/client && "$(FLUTTER)" test
	cd services/research && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m unittest discover -s tests -v
	$(MAKE) --no-print-directory test-resource-cleanup

test-resource-cleanup:
	@set -eu; \
	stale_root=; stale_group_root=; orphan_group_root=; active_root=; reused_pid_root=; reused_start_root=; stale_pid=; stale_group_pid=; orphan_group_pgid=; \
	cleanup() { \
		if test -n "$$stale_pid"; then kill "$$stale_pid" 2>/dev/null || true; wait "$$stale_pid" 2>/dev/null || true; fi; \
		if test -n "$$stale_group_pid"; then /bin/kill -TERM -- "-$$stale_group_pid" 2>/dev/null || true; wait "$$stale_group_pid" 2>/dev/null || true; fi; \
		if test -n "$$orphan_group_pgid"; then /bin/kill -TERM -- "-$$orphan_group_pgid" 2>/dev/null || true; fi; \
		for root in "$$stale_root" "$$stale_group_root" "$$orphan_group_root" "$$active_root" "$$reused_pid_root" "$$reused_start_root"; do test -z "$$root" || test ! -e "$$root" || find "$$root" -depth -delete 2>/dev/null || true; done; \
	}; \
	trap cleanup EXIT INT TERM; \
	stale_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	stale_group_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-test.XXXXXX")"; \
	orphan_group_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-test.XXXXXX")"; \
	active_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	reused_pid_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	reused_start_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	ln -s /bin/sleep "$$stale_root/omni-core"; \
	"$$stale_root/omni-core" 60 & stale_pid=$$!; \
	kill -0 "$$stale_pid"; \
	printf '%s\n' 2147483647 >"$$stale_root/.owner-pid"; \
	printf '%s\n' '/definitely/not/running' >"$$stale_root/.owner-command"; \
	printf '%s\n' "$$stale_pid" >"$$stale_root/.server-pid"; \
	: >"$$stale_group_root/.child-starting"; \
	"$(PYTHON)" -c 'import os,pathlib,subprocess,sys; os.setsid(); root=pathlib.Path(sys.argv[1]); root.joinpath(".child-pgid").write_text(f"{os.getpid()}\n"); started=" ".join(subprocess.check_output(["ps","-p",str(os.getpid()),"-o","lstart="], text=True).split()); root.joinpath(".child-start").write_text(started+"\n"); root.joinpath(".child-starting").unlink(missing_ok=True); os.execvp(sys.argv[2], sys.argv[2:])' "$$stale_group_root" /bin/sleep 60 & stale_group_pid=$$!; \
	for _ in $$(seq 1 50); do test -f "$$stale_group_root/.child-start" && break; sleep 0.02; done; \
	test -f "$$stale_group_root/.child-pgid"; test -f "$$stale_group_root/.child-start"; \
	kill -0 "$$stale_group_pid"; \
	printf '%s\n' 2147483647 >"$$stale_group_root/.owner-pid"; \
	printf '%s\n' '/definitely/not/running' >"$$stale_group_root/.owner-command"; \
	: >"$$orphan_group_root/.child-starting"; \
	"$(PYTHON)" -c 'import os,pathlib,subprocess,sys,time; os.setsid(); root=pathlib.Path(sys.argv[1]); pgid=os.getpid(); root.joinpath(".child-pgid").write_text(f"{pgid}\n"); started=" ".join(subprocess.check_output(["ps","-p",str(pgid),"-o","lstart="], text=True).split()); root.joinpath(".child-start").write_text(started+"\n"); child=os.fork(); root.joinpath(".child-starting").unlink(missing_ok=True); (root.joinpath(".orphan-pid").write_text(f"{child}\n"), os._exit(0)) if child else time.sleep(60)' "$$orphan_group_root" & orphan_launcher_pid=$$!; \
	wait "$$orphan_launcher_pid"; \
	for _ in $$(seq 1 50); do test -f "$$orphan_group_root/.orphan-pid" && break; sleep 0.02; done; \
	orphan_group_pgid="$$(sed -n '1p' "$$orphan_group_root/.child-pgid")"; \
	orphan_child_pid="$$(sed -n '1p' "$$orphan_group_root/.orphan-pid")"; \
	/bin/kill -0 -- "-$$orphan_group_pgid"; kill -0 "$$orphan_child_pid"; \
	test -z "$$(ps -p "$$orphan_group_pgid" -o lstart= 2>/dev/null | awk '{$$1=$$1; print}')"; \
	printf '%s\n' 2147483647 >"$$orphan_group_root/.owner-pid"; \
	printf '%s\n' '/definitely/not/running' >"$$orphan_group_root/.owner-command"; \
	printf '%s\n' "$$$$" >"$$active_root/.owner-pid"; \
	ps -p "$$$$" -o command= >"$$active_root/.owner-command"; \
	ps -p "$$$$" -o lstart= | awk '{$$1=$$1; print}' >"$$active_root/.owner-start"; \
	touch "$$active_root/active-marker"; \
	printf '%s\n' "$$$$" >"$$reused_pid_root/.owner-pid"; \
	printf '%s\n' '/pid/reused/by/another/process' >"$$reused_pid_root/.owner-command"; \
	printf '%s\n' "$$$$" >"$$reused_start_root/.owner-pid"; \
	ps -p "$$$$" -o command= >"$$reused_start_root/.owner-command"; \
	printf '%s\n' 'Mon Jan  1 00:00:00 2001' >"$$reused_start_root/.owner-start"; \
	$(MAKE) --no-print-directory clean-test-resources; \
	wait "$$stale_pid" 2>/dev/null || true; stale_pid=; \
	if /bin/kill -0 -- "-$$stale_group_pid" 2>/dev/null; then echo 'stale child process group remains' >&2; exit 1; fi; \
	wait "$$stale_group_pid" 2>/dev/null || true; stale_group_pid=; \
	test ! -e "$$stale_root"; \
	test ! -e "$$stale_group_root"; \
	if /bin/kill -0 -- "-$$orphan_group_pgid" 2>/dev/null; then echo 'orphaned child process group remains' >&2; exit 1; fi; \
	orphan_group_pgid=; \
	test ! -e "$$orphan_group_root"; \
	test -e "$$active_root/active-marker"; \
	test ! -e "$$reused_pid_root"; \
	test ! -e "$$reused_start_root"

contract-check:
	"$(PYTHON)" -c 'import json, pathlib; files=sorted(pathlib.Path("contracts").rglob("*.json")); assert files, "no JSON contract files found"; [json.loads(path.read_text(encoding="utf-8")) for path in files]; print(f"validated {len(files)} JSON contract files")'

check:
	+@check_root=; \
	cleanup() { \
		cleanup_status=0; \
		trap - EXIT INT TERM; \
		if test -n "$$child_pid"; then /bin/kill -TERM -- "-$$child_pid" 2>/dev/null || true; wait "$$child_pid" 2>/dev/null || true; fi; \
		if test -n "$$check_root"; then \
			$(MAKE) --no-print-directory clean-test-resources TEST_SESSION_ROOT="$$check_root" || cleanup_status=$$?; \
		else \
			$(MAKE) --no-print-directory clean-test-resources || cleanup_status=$$?; \
		fi; \
		if test "$$status" -ne 0; then exit "$$status"; fi; \
		exit "$$cleanup_status"; \
	}; \
	child_pid=; \
	status=0; \
	trap 'status=130; cleanup' INT; \
	trap 'status=143; cleanup' TERM; \
	trap 'status=$$?; cleanup' EXIT; \
	check_root="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-test.XXXXXX")"; \
	printf '%s\n' "$$$$" >"$$check_root/.owner-pid"; \
	ps -p "$$$$" -o command= >"$$check_root/.owner-command"; \
	ps -p "$$$$" -o lstart= | awk '{$$1=$$1; print}' >"$$check_root/.owner-start"; \
	: >"$$check_root/.child-starting"; \
	"$(PYTHON)" -c 'import os,pathlib,subprocess,sys; os.setsid(); root=pathlib.Path(sys.argv[1]); root.joinpath(".child-pgid").write_text(f"{os.getpid()}\n"); started=" ".join(subprocess.check_output(["ps","-p",str(os.getpid()),"-o","lstart="], text=True).split()); root.joinpath(".child-start").write_text(started+"\n"); root.joinpath(".child-starting").unlink(missing_ok=True); os.execvp(sys.argv[2], sys.argv[2:])' "$$check_root" "$(MAKE)" format-check lint test contract-check & \
	child_pid=$$!; \
	child_status=0; \
	wait "$$child_pid" || child_status=$$?; \
	child_pid=; \
	exit "$$child_status"

clean-test-resources:
	@set -eu; \
	stop_child_group() { \
		session_path="$$1"; \
		if test -f "$$session_path/.child-starting"; then \
			for _ in $$(seq 1 100); do \
				test -f "$$session_path/.child-pgid" -a -f "$$session_path/.child-start" && break; \
				test -f "$$session_path/.child-starting" || break; \
				sleep 0.02; \
			done; \
		fi; \
		if test ! -f "$$session_path/.child-pgid" -o ! -f "$$session_path/.child-start"; then \
			test ! -f "$$session_path/.child-starting" || { echo "owned child process metadata is incomplete: $$session_path" >&2; return 1; }; \
			return 0; \
		fi; \
		child_pgid="$$(sed -n '1p' "$$session_path/.child-pgid")"; \
		case "$$child_pgid" in ''|*[!0-9]*) echo "invalid owned child process group: $$session_path" >&2; return 1;; esac; \
		/bin/kill -0 -- "-$$child_pgid" 2>/dev/null || return 0; \
		recorded_start="$$(sed -n '1p' "$$session_path/.child-start")"; \
		test -n "$$recorded_start" || { echo "invalid owned child process start: $$session_path" >&2; return 1; }; \
		current_start="$$(ps -p "$$child_pgid" -o lstart= 2>/dev/null | awk '{$$1=$$1; print}')"; \
		if test -n "$$current_start" -a "$$current_start" != "$$recorded_start"; then echo "refusing reused child process group: $$child_pgid" >&2; return 1; fi; \
		/bin/kill -TERM -- "-$$child_pgid" 2>/dev/null || true; \
		for _ in $$(seq 1 50); do /bin/kill -0 -- "-$$child_pgid" 2>/dev/null || return 0; sleep 0.02; done; \
		current_start="$$(ps -p "$$child_pgid" -o lstart= 2>/dev/null | awk '{$$1=$$1; print}')"; \
		if test -n "$$current_start" -a "$$current_start" != "$$recorded_start"; then echo "owned child group changed before KILL: $$child_pgid" >&2; return 1; fi; \
		/bin/kill -KILL -- "-$$child_pgid" 2>/dev/null || true; \
		for _ in $$(seq 1 50); do /bin/kill -0 -- "-$$child_pgid" 2>/dev/null || return 0; sleep 0.02; done; \
		echo "owned child process group remains: $$child_pgid" >&2; return 1; \
	}; \
	if test -n "$${TEST_SESSION_ROOT:-}"; then \
		case "$$TEST_SESSION_ROOT" in "$${TMPDIR:-/tmp}"/omni-folio-test.*) ;; *) echo "refusing unsafe test session root: $$TEST_SESSION_ROOT" >&2; exit 1;; esac; \
		stop_child_group "$$TEST_SESSION_ROOT"; \
		if test -d "$$TEST_SESSION_ROOT"; then find "$$TEST_SESSION_ROOT" -depth -delete 2>/dev/null || test ! -e "$$TEST_SESSION_ROOT"; fi; \
	fi; \
	for session_path in "$${TMPDIR:-/tmp}"/omni-folio-test.* "$${TMPDIR:-/tmp}"/omni-folio-smoke.*; do \
		test -d "$$session_path" || continue; \
		owner_file="$$session_path/.owner-pid"; \
		if test ! -f "$$owner_file"; then rmdir "$$session_path" 2>/dev/null || true; continue; fi; \
		owner_pid="$$(sed -n '1p' "$$owner_file")"; \
		case "$$owner_pid" in ''|*[!0-9]*) continue;; esac; \
		owner_active=0; \
		if kill -0 "$$owner_pid" 2>/dev/null; then \
			owner_command_file="$$session_path/.owner-command"; \
			if test -f "$$owner_command_file"; then \
				owner_command="$$(ps -p "$$owner_pid" -o command= 2>/dev/null || true)"; \
				expected_owner_command="$$(sed -n '1p' "$$owner_command_file")"; \
				if test "$$owner_command" = "$$expected_owner_command"; then \
					owner_start_file="$$session_path/.owner-start"; \
					if test -f "$$owner_start_file"; then \
						owner_start="$$(ps -p "$$owner_pid" -o lstart= 2>/dev/null | awk '{$$1=$$1; print}')"; \
						expected_owner_start="$$(sed -n '1p' "$$owner_start_file")"; \
						if test -n "$$owner_start" -a "$$owner_start" = "$$expected_owner_start"; then owner_active=1; fi; \
					else \
						owner_active=1; \
					fi; \
				fi; \
			else \
				owner_active=1; \
			fi; \
		fi; \
		if test "$$owner_active" -eq 0; then \
			stop_child_group "$$session_path"; \
			server_file="$$session_path/.server-pid"; \
			if test -f "$$server_file"; then \
				server_pid="$$(sed -n '1p' "$$server_file")"; \
				case "$$server_pid" in ''|*[!0-9]*) server_pid=;; esac; \
				if test -n "$$server_pid"; then \
					server_command="$$(ps -p "$$server_pid" -o command= 2>/dev/null || true)"; \
					case "$$server_command" in "$$session_path/omni-core"*) kill "$$server_pid" 2>/dev/null || true;; esac; \
				fi; \
			fi; \
			if test -d "$$session_path"; then find "$$session_path" -depth -delete 2>/dev/null || test ! -e "$$session_path"; fi; \
		fi; \
	done; \
	for artifact_path in \
		"$(ROOT)/apps/client/build" \
		"$(ROOT)/apps/client/coverage" \
		"$(ROOT)/services/core/core"; do \
		if test -e "$$artifact_path"; then find "$$artifact_path" -depth -delete; fi; \
	done; \
	find "$(ROOT)/services/research" -type f \( -name '*.pyc' -o -name '*.pyo' \) -exec /bin/unlink {} \; ; \
	find "$(ROOT)/services/research" -depth -type d -name __pycache__ -exec rmdir {} \; ; \
	test -z "$$(find "$(ROOT)/services/research" -type f \( -name '*.pyc' -o -name '*.pyo' \) -print -quit)" || { echo "owned Python bytecode remains" >&2; exit 1; }; \
	test -z "$$(find "$(ROOT)/services/research" -type d -name __pycache__ -print -quit)" || { echo "owned Python cache directory remains" >&2; exit 1; }; \
	for artifact_path in "$(ROOT)/apps/client/build" "$(ROOT)/apps/client/coverage" "$(ROOT)/services/core/core"; do \
		test ! -e "$$artifact_path" || { echo "owned test artifact remains: $$artifact_path" >&2; exit 1; }; \
	done

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

seed-demo:
	@set -eu; \
	preview_json="$$(curl --fail --silent -X POST -H 'Content-Type: text/csv' --data-binary @"$(SEED_DEMO_CSV)" "$(API_URL)/v1/imports/preview")"; \
	preview_state="$$(printf '%s' "$$preview_json" | "$(PYTHON)" -c 'import json,sys; p=json.load(sys.stdin); t=p.get("totals"); rows=p.get("rows"); fail=lambda message: sys.exit(message); (p.get("can_apply") is True) or fail("demo preview cannot apply"); isinstance(t,dict) or fail("demo preview has invalid totals"); isinstance(rows,list) or fail("demo preview has invalid rows"); t.get("error_rows") == 0 or fail("demo preview has errors"); t.get("unresolved_rows") == 0 or fail("demo preview has unresolved rows"); new=t.get("new_rows"); isinstance(new,int) and new >= 0 or fail("demo preview has invalid new row count"); noop=new == 0; (not noop or (rows and t.get("duplicate_rows") == len(rows) and all(isinstance(row,dict) and row.get("status") == "duplicate" for row in rows))) or fail("demo preview is not an exact duplicate replay"); print("noop" if noop else "apply")')"; \
	new_rows="$$(printf '%s' "$$preview_json" | "$(PYTHON)" -c 'import json,sys; print(json.load(sys.stdin)["totals"]["new_rows"])')"; \
	if test "$$preview_state" = noop; then printf '%s\n' 'demo: sample ledger already present'; exit 0; fi; \
	preview_id="$$(printf '%s' "$$preview_json" | "$(PYTHON)" -c 'import json,sys; print(json.load(sys.stdin)["preview_id"])')"; \
	request_json="$$("$(PYTHON)" -c 'import json,sys; print(json.dumps({"preview_id":sys.argv[1],"idempotency_key":"demo-"+sys.argv[1]}))' "$$preview_id")"; \
	apply_json="$$(curl --fail --silent -X POST -H 'Content-Type: application/json' --data "$$request_json" "$(API_URL)/v1/imports/apply")"; \
	printf '%s' "$$apply_json" | "$(PYTHON)" -c 'import json,sys; d=json.load(sys.stdin); expected=int(sys.argv[1]); applied=d.get("applied_rows"); (isinstance(applied,int) and applied == expected and applied > 0) or sys.exit("demo apply row count mismatch"); print("demo: applied", applied, "sample rows at", d.get("ledger_revision_after"))' "$$new_rows"

run-research:
	@PYTHONDONTWRITEBYTECODE=1 PYTHONPATH="$(RESEARCH_PYTHONPATH)" "$(PYTHON)" -m omni_research \
		--bars "$(ROOT)/contracts/fixtures/market-bars.csv" \
		--request "$(ROOT)/contracts/fixtures/backtest-request.json" \
		--run-id run_buy_hold_fixture_001 \
		--dataset-id dataset_market_bars_fixture \
		--started-at 2026-08-23T06:00:00Z \
		--finished-at 2026-08-23T06:00:00.010Z

run-improvement:
	@PYTHONDONTWRITEBYTECODE=1 PYTHONPATH="$(RESEARCH_PYTHONPATH)" "$(PYTHON)" -m omni_research.improve_cli \
		--bars "$(ROOT)/contracts/fixtures/strategy-market-bars.csv" \
		--config "$(ROOT)/contracts/fixtures/strategy-improvement-config.json"

smoke:
	@set -eu; \
	$(MAKE) --no-print-directory clean-test-resources; \
	smoke_dir=; \
	status=0; \
	pid=; \
	child_pid=; \
	cleanup() { \
		cleanup_status=0; \
		trap - EXIT INT TERM; \
		if test -n "$$child_pid"; then /bin/kill -TERM -- "-$$child_pid" 2>/dev/null || true; wait "$$child_pid" 2>/dev/null || true; fi; \
		if test -n "$$pid"; then kill "$$pid" 2>/dev/null || true; wait "$$pid" 2>/dev/null || true; fi; \
		if test -n "$$smoke_dir"; then \
			case "$$smoke_dir" in "$${TMPDIR:-/tmp}"/omni-folio-smoke.*) test ! -e "$$smoke_dir" || find "$$smoke_dir" -depth -delete 2>/dev/null || test ! -e "$$smoke_dir" || cleanup_status=$$?;; *) echo "refusing unsafe smoke root: $$smoke_dir" >&2; cleanup_status=1;; esac; \
		fi; \
		if test "$$status" -ne 0; then exit "$$status"; fi; \
		exit "$$cleanup_status"; \
	}; \
	trap 'status=130; cleanup' INT; \
	trap 'status=143; cleanup' TERM; \
	trap 'status=$$?; cleanup' EXIT; \
	smoke_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/omni-folio-smoke.XXXXXX")"; \
	printf '%s\n' "$$$$" >"$$smoke_dir/.owner-pid"; \
	ps -p "$$$$" -o command= >"$$smoke_dir/.owner-command"; \
	ps -p "$$$$" -o lstart= | awk '{$$1=$$1; print}' >"$$smoke_dir/.owner-start"; \
	db="$$smoke_dir/omni-folio.db"; \
	bin="$$smoke_dir/omni-core"; \
	log="$$smoke_dir/core.log"; \
	: >"$$smoke_dir/.child-starting"; \
	"$(PYTHON)" -c 'import os,pathlib,subprocess,sys; os.setsid(); root=pathlib.Path(sys.argv[1]); root.joinpath(".child-pgid").write_text(f"{os.getpid()}\n"); started=" ".join(subprocess.check_output(["ps","-p",str(os.getpid()),"-o","lstart="], text=True).split()); root.joinpath(".child-start").write_text(started+"\n"); root.joinpath(".child-starting").unlink(missing_ok=True); os.chdir(sys.argv[2]); os.execvp(sys.argv[3], sys.argv[3:])' "$$smoke_dir" "$(ROOT)/services/core" "$(GO)" build -o "$$bin" . & \
	child_pid=$$!; \
	child_status=0; \
	wait "$$child_pid" || child_status=$$?; \
	child_pid=; \
	test "$$child_status" -eq 0 || exit "$$child_status"; \
	"$$bin" migrate -db "$$db"; \
	"$$bin" serve -db "$$db" -addr 127.0.0.1:18080 -market-fixture "$(MARKET_FIXTURE)" >"$$log" 2>&1 & \
	pid=$$!; \
	printf '%s\n' "$$pid" >"$$smoke_dir/.server-pid"; \
	ready=0; \
	for _ in $$(seq 1 30); do \
		if curl --fail --silent http://127.0.0.1:18080/healthz >/dev/null; then ready=1; break; fi; \
		sleep 1; \
	done; \
	test "$$ready" -eq 1 || { cat "$$log" >&2; exit 1; }; \
	curl --fail --silent http://127.0.0.1:18080/readyz >/dev/null; \
	status_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/status)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["service"] == "omni-folio" and d["ledger_revision"] == "rev_0000000000" and d["live_enabled"] is False' "$$status_json"; \
	$(MAKE) --no-print-directory seed-demo API_URL=http://127.0.0.1:18080; \
	snapshot_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/portfolio/snapshot)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["ledger_revision"] == "rev_0000000003" and d["cost_basis_policy"] == "fifo_exact_else_half_even_residual_8_v1" and d["live_enabled"] is False and d["cash"][0]["amount"] == "778"' "$$snapshot_json"; \
	duplicate_output="$$( $(MAKE) --no-print-directory seed-demo API_URL=http://127.0.0.1:18080 )"; \
	test "$$duplicate_output" = 'demo: sample ledger already present'; \
	conflict_csv="$$smoke_dir/conflicting-import.csv"; \
	sed 's/,USD,1000$$/,USD,1001/' contracts/fixtures/golden-import.csv > "$$conflict_csv"; \
	if PYTHONOPTIMIZE=1 $(MAKE) --no-print-directory seed-demo API_URL=http://127.0.0.1:18080 SEED_DEMO_CSV="$$conflict_csv" >/dev/null 2>&1; then echo 'seed-demo accepted a conflicting preview' >&2; exit 1; fi; \
	activity_json="$$(curl --fail --silent http://127.0.0.1:18080/v1/ledger/activities)"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["source"] == "local_ledger" and d["broker_freshness"] == "unverified" and d["ledger_revision"] == "rev_0000000003" and [row["type"] for row in d["events"]] == ["SELL", "BUY", "DEPOSIT"] and d["next_cursor"] is None; assert not ({"event_id", "source_event_id", "account_id", "instrument_id", "receipt_id", "corrects_source_event_id", "sequence"} & set().union(*(row.keys() for row in d["events"])))' "$$activity_json"; \
	market_json="$$(curl --fail --silent 'http://127.0.0.1:18080/v1/market-data/candles?symbol=AAPL&interval=1d')"; \
	"$(PYTHON)" -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["symbol"] == "AAPL" and d["price_adjustment"] == "unspecified" and d["source"] == "local_fixture" and d["sample"] is True and d["state"] == "stale" and d["issues"][0]["code"] == "sample_data" and len(d["bars"]) == 6 and d["bars"][0]["open"] == "10" and d["bars"][-1]["close"] == "16"' "$$market_json"; \
	printf '%s\n' 'smoke: health, status, preview, apply, snapshot, activity, market data OK'
