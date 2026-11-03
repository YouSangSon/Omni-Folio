from __future__ import annotations

import csv
import copy
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from omni_research.engine import Bar, canonical_hash
from omni_research.improve import Parameters, parse_config, run_experiment, simulate_fold

ROOT = Path(__file__).parents[3]


def synthetic_capture() -> dict:
    capture = json.loads((ROOT / "contracts/fixtures/kiwoom-candle-capture.json").read_text())
    rows = csv.DictReader(io.StringIO((ROOT / "contracts/fixtures/strategy-market-bars.csv").read_text()))
    capture["bars"] = [
        {"provider_time": row["bar_at"][:10].replace("-", ""), **{key: row[key] for key in ("open", "high", "low", "close", "volume")}}
        for row in rows
    ]
    return capture


class CaptureResearchTest(unittest.TestCase):
    def config(self) -> dict:
        return json.loads((ROOT / "contracts/fixtures/strategy-improvement-paper-config.json").read_text())

    def research(self, capture: dict, config: dict | None = None) -> dict:
        from omni_research.capture_research import run_capture_experiment
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "capture.json"
            path.write_text(json.dumps(capture), encoding="utf-8")
            return run_capture_experiment(path, self.config() if config is None else config)

    def native(self, raw: bytes, config: bytes | None = None, extra: tuple[str, ...] = ()) -> subprocess.CompletedProcess:
        with tempfile.TemporaryDirectory() as temporary:
            capture = Path(temporary) / "capture.json"
            configuration = Path(temporary) / "config.json"
            capture.write_bytes(raw)
            configuration.write_bytes(json.dumps(self.config()).encode() if config is None else config)
            before_config = configuration.read_bytes()
            result = subprocess.run(
                [sys.executable, "-B", "-m", "omni_research.capture_cli", "--capture", str(capture), "--config", str(configuration), *extra],
                env={**os.environ, "PYTHONPATH": str(ROOT / "services/research"), "PYTHONDONTWRITEBYTECODE": "1"},
                capture_output=True, timeout=15,
            )
            self.assertEqual(capture.read_bytes(), raw)
            self.assertEqual(configuration.read_bytes(), before_config)
            return result

    def test_native_cli_emits_a_closed_research_report(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            capture = Path(temporary) / "synthetic-capture.json"
            capture.write_text(json.dumps(synthetic_capture()), encoding="utf-8")
            result = subprocess.run(
                [sys.executable, "-B", "-m", "omni_research.capture_cli", "--capture", str(capture),
                 "--config", str(ROOT / "contracts/fixtures/strategy-improvement-paper-config.json")],
                env={**os.environ, "PYTHONPATH": str(ROOT / "services/research"), "PYTHONDONTWRITEBYTECODE": "1"},
                capture_output=True, timeout=15,
            )
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertEqual(result.stderr, b"")
        report = json.loads(result.stdout)
        self.assertEqual(report["schema_version"], "captured-candle-research.v1")
        self.assertEqual(report["scope"], "offline_capture_research_only")
        self.assertEqual(set(report), {"schema_version", "scope", "source", "config", "config_sha256", "analysis", "limitations", "report_sha256"})

    def test_all_numeric_results_and_selections_equal_existing_v2(self) -> None:
        capture = synthetic_capture()
        report = self.research(capture)
        legacy = run_experiment(ROOT / "contracts/fixtures/strategy-market-bars.csv", self.config())
        analysis = report["analysis"]
        without_lookahead = lambda metrics: {key: value for key, value in metrics.items() if key != "lookahead_violations"}
        self.assertEqual(analysis["candidates"], [
            {"parameters": item["parameters"], "pre_holdout_metrics": without_lookahead(item["pre_holdout_metrics"])}
            for item in legacy["candidates"]
        ])
        challenger = legacy["challenger"]
        self.assertEqual(analysis["selected_parameters"], challenger["parameters"])
        self.assertEqual(analysis["selected_pre_holdout_metrics"], without_lookahead(challenger["pre_holdout_metrics"]))
        self.assertEqual(analysis["validation_metrics"], without_lookahead(challenger["walk_forward_metrics"]))
        self.assertEqual(analysis["final_holdout"]["metrics"], without_lookahead(challenger["final_holdout_metrics"]))
        self.assertEqual(analysis["buy_and_hold_metrics"], without_lookahead(legacy["promotion"]["baseline"]["final_holdout_metrics"]))
        for actual, expected in zip(analysis["folds"], challenger["walk_forward_folds"], strict=True):
            self.assertEqual(actual["name"], expected["name"])
            self.assertEqual(actual["selected_parameters"], expected["selected_parameters"])
            for name in ("train_metrics", "validation_metrics"):
                self.assertEqual(actual[name], without_lookahead(expected[name]))
            for name in ("train", "validation"):
                span = actual[name]
                self.assertEqual(span["bar_count"], expected[name]["bar_count"])
                self.assertEqual(span["first"]["provider_time"], expected[name]["first_bar_at"][:10].replace("-", ""))
                self.assertEqual(span["last"]["provider_time"], expected[name]["last_bar_at"][:10].replace("-", ""))
                self.assertEqual(span["last"]["row"] - span["first"]["row"] + 1, span["bar_count"])
        for actual, expected in zip(analysis["final_holdout"]["trades"], challenger["final_holdout_trades"], strict=True):
            self.assertEqual({key: actual[key] for key in ("side", "quantity", "price")}, {key: expected[key] for key in ("side", "quantity", "price")})
            for key in ("signal", "eligible", "fill"):
                reference = actual[key]
                self.assertEqual(reference["provider_time"], expected[key + "_at"][:10].replace("-", ""))
                self.assertEqual(reference["provider_time"], capture["bars"][reference["row"]]["provider_time"])
            self.assertLess(actual["signal"]["row"], actual["eligible"]["row"])
            self.assertLessEqual(actual["eligible"]["row"], actual["fill"]["row"])
        self.assertEqual(analysis["final_holdout"]["range"], {"first": {"row": 90, "provider_time": capture["bars"][90]["provider_time"]}, "last": {"row": 119, "provider_time": capture["bars"][119]["provider_time"]}, "bar_count": 30})
        self.assertEqual(analysis["threshold_comparisons"], {
            "walk_forward_thresholds_met": legacy["promotion"]["walk_forward_gate_passed"],
            "final_holdout_thresholds_met": legacy["promotion"]["final_holdout_gate_passed"],
            "challenger_exceeds_buy_and_hold": legacy["promotion"]["baseline_gate_passed"],
        })
        self.assertNotIn("lookahead_violations", json.dumps(report))
        self.assertNotIn("promotion", analysis)

    def test_holdout_never_selects_and_raw_labels_are_not_instants(self) -> None:
        capture = synthetic_capture()
        original = self.research(capture)
        reversed_prices = list(reversed([{key: row[key] for key in ("open", "high", "low", "close")} for row in capture["bars"][90:]]))
        for row, prices in zip(capture["bars"][90:], reversed_prices, strict=True):
            row.update(prices)
        changed = self.research(capture)
        for key in ("candidates", "folds", "selected_parameters", "selected_pre_holdout_metrics", "validation_metrics"):
            self.assertEqual(original["analysis"][key], changed["analysis"][key])
        self.assertNotEqual(original["analysis"]["final_holdout"]["metrics"], changed["analysis"]["final_holdout"]["metrics"])
        for forbidden in ('"at"', '"signal_at"', '"eligible_at"', '"fill_at"', '"source_available_at"', '"close_at"', '"instrument_id"', '"manifest"', '"target"', '"proposal"'):
            self.assertNotIn(forbidden, json.dumps(original))

    def test_snapshot_hashes_preserve_original_bytes_strings_and_config(self) -> None:
        capture = synthetic_capture()
        capture["fetch_started_at"] = "2030-01-01T00:00:00.123456789Z"
        capture["fetched_at"] = "2030-01-01T00:00:00.123456790Z"
        raw = json.dumps(capture, indent=3).encode() + b"\n"
        first = self.native(raw)
        second = self.native(raw)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(first.stdout, second.stdout)
        report = json.loads(first.stdout)
        self.assertEqual(report["source"], {"sha256": hashlib.sha256(raw).hexdigest(), "capture": capture})
        self.assertEqual(report["config"], self.config())
        self.assertEqual(report["config_sha256"], canonical_hash(self.config()))
        self.assertEqual(report["report_sha256"], canonical_hash({key: value for key, value in report.items() if key != "report_sha256"}))
        self.assertEqual(first.stdout, (json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode())
        self.assertLess(len(first.stdout), 1_048_576)
        reformatted = json.loads(self.native(json.dumps(capture).encode()).stdout)
        self.assertEqual(reformatted["analysis"], report["analysis"])
        self.assertNotEqual(reformatted["source"]["sha256"], report["source"]["sha256"])
        config = self.config()
        config["promotion"]["minimum_holdout_after_cost_return"] = "999"
        changed = self.research(capture, config)
        self.assertNotEqual(changed["config_sha256"], report["config_sha256"])
        self.assertFalse(changed["analysis"]["threshold_comparisons"]["final_holdout_thresholds_met"])

    def test_native_rejects_malformed_duplicate_and_unsafe_numeric_inputs_without_output(self) -> None:
        raw = json.dumps(synthetic_capture()).encode()
        invalid = [
            b"", b"[]", b"null", b"\xff", raw + b" {}", raw[:-1],
            raw.replace(b'"provider": "kiwoom"', b'"provider": "kiwoom", "provider": "kiwoom"', 1),
            raw.replace(b'"api_id": "ka10081"', b'"api_id": "ka10081", "api_id": "ka10081"', 1),
            raw.replace(b'"pages_limit": 32', b'"pages_limit": NaN'),
            raw.replace(b'"pages_limit": 32', b'"pages_limit": Infinity'),
            raw.replace(b'"pages_limit": 32', b'"pages_limit": 32.0'),
            b" " * 1_048_577,
        ]
        for field, value in (("open", "1e999999999"), ("close", "NaN"), ("volume", "-0"), ("volume", "01"), ("volume", "1.0"), ("volume", "9" * 65), ("close", "0"), ("open", 10)):
            capture = synthetic_capture()
            capture["bars"][0][field] = value
            invalid.append(json.dumps(capture).encode())
        for value in invalid:
            with self.subTest(raw=value[:120]):
                result = self.native(value)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b"")
                self.assertIn(b"invalid", result.stderr)

    def test_native_rejects_config_duplicates_exponents_and_report_overflow(self) -> None:
        raw = json.dumps(synthetic_capture()).encode()
        config_bytes = json.dumps(self.config()).encode()
        invalid = [
            config_bytes.replace(b'"fee": "1"', b'"fee": "1", "fee": "1"'),
            config_bytes.replace(b'"fee": "1"', b'"fee": "1e999999999"'),
            config_bytes.replace(b'"fee": "1"', b'"fee": 1'),
            config_bytes.replace(b'"fee": "1"', b'"fee": "-0"'),
            config_bytes.replace(b'"train_bars": 30', b'"train_bars": true'),
            b" " * 1_048_577,
        ]
        for version in ("strategy-improvement.v1", "unknown"):
            config = self.config()
            config["schema_version"] = version
            invalid.append(json.dumps(config).encode())
        oversized = self.config()
        oversized["experiment_id"] = "x" * 1_035_000
        encoded = json.dumps(oversized).encode()
        self.assertLess(len(encoded), 1_048_576)
        invalid.append(encoded)
        for value in invalid:
            with self.subTest(config=value[:120]):
                result = self.native(raw, value)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b"")

    def test_config_numeric_strings_do_not_inherit_source_token_limit(self) -> None:
        config = self.config()
        config["execution"]["starting_cash"] = "1" + "0" * 70
        report = self.research(synthetic_capture(), config)
        self.assertEqual(report["config"], config)
        capture = synthetic_capture()
        capture["bars"][0]["volume"] = "1" + "0" * 63
        self.assertEqual(self.research(capture)["source"]["capture"], capture)

    def test_native_coherence_daily_profile_and_nanosecond_order(self) -> None:
        mutations = [
            (("interval",), "1m"), (("symbol",), "005931"), (("symbol",), "００５９３０"),
            (("request", "api_id"), "ka10080"), (("request", "body", "upd_stkpc_tp"), "0"),
            (("request", "body", "base_dt"), "20290229"), (("request", "body", "base_dt"), "20300102"),
            (("time_evidence", "provider_timezone"), "Asia/Seoul"), (("time_evidence", "bar_completion"), "complete"),
            (("bounds", "bars_limit"), 499), (("bounds", "pages_limit"), True),
            (("fetch_started_at",), "2030-01-01T00:00:00+00:00"), (("fetched_at",), "2030-02-30T00:00:00Z"),
            (("fetched_at",), "2029-12-31T23:59:59Z"),
        ]
        for path, value in mutations:
            capture = synthetic_capture()
            target = capture
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
            with self.subTest(path=path, value=value):
                result = self.native(json.dumps(capture).encode())
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b"")
        capture = synthetic_capture()
        capture["fetch_started_at"] = "2030-01-01T00:00:00.123456789Z"
        capture["fetched_at"] = "2030-01-01T00:00:00.123456788Z"
        result = self.native(json.dumps(capture).encode())
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b"")
        capture["fetch_started_at"] = "2029-12-31T15:00:00.000000001Z"
        capture["fetched_at"] = "2029-12-31T15:00:00.000000002Z"
        self.assertEqual(self.research(capture)["source"]["capture"], capture)
        capture["fetch_started_at"] = "2029-12-31T14:59:59.999999999Z"
        with self.assertRaisesRegex(ValueError, "fixed UTC"):
            self.research(capture)

    def test_closed_objects_rows_prices_precision_and_exact_fold_count(self) -> None:
        for path in ((), ("request",), ("request", "body"), ("time_evidence",), ("bounds",)):
            capture = synthetic_capture()
            target = capture
            for key in path:
                target = target[key]
            target["unexpected"] = "x"
            with self.subTest(path=path), self.assertRaises(ValueError):
                self.research(capture)
        for value in ("20260230", "2026-01-01", "２０２６０１０１", "20260101000000", "20300102", None):
            capture = synthetic_capture()
            capture["bars"][0]["provider_time"] = value
            with self.subTest(date=value), self.assertRaises(ValueError):
                self.research(capture)
        for mode in ("duplicate", "reverse", "short", "empty", "extra-row", "extra-field", "negative-volume", "inconsistent", "close-limit", "close-precision"):
            capture = synthetic_capture()
            if mode == "duplicate": capture["bars"][1]["provider_time"] = capture["bars"][0]["provider_time"]
            if mode == "reverse": capture["bars"].reverse()
            if mode == "short": capture["bars"].pop()
            if mode == "empty": capture["bars"] = []
            if mode == "extra-row": capture["bars"] *= 5
            if mode == "extra-field": capture["bars"][0]["at"] = "2026-01-01T00:00:00Z"
            if mode == "negative-volume": capture["bars"][0]["volume"] = "-1"
            if mode == "inconsistent": capture["bars"][0]["high"] = "1"
            if mode == "close-limit": capture["bars"][0].update(open="1000000000000", high="1000000000000", low="1000000000000", close="1000000000000")
            if mode == "close-precision": capture["bars"][0]["close"] = "20.000000001"
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                self.research(capture)

    def test_opened_descriptor_single_snapshot_and_nonregular_inputs(self) -> None:
        from omni_research import capture_research
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "capture.json"
            raw = json.dumps(synthetic_capture()).encode()
            path.write_bytes(raw)
            original_open = open
            reads = []
            class ObservedFile:
                def __enter__(self): return self
                def __exit__(self, *args): self.source.close()
                def fileno(self): return self.source.fileno()
                def read(self, limit):
                    reads.append(limit)
                    result = self.source.read(limit)
                    path.write_bytes(b"replaced")
                    return result
            def observed_open(*args, **kwargs):
                observed = ObservedFile()
                observed.source = original_open(*args, **kwargs)
                return observed
            with patch.object(capture_research, "open", side_effect=observed_open):
                report = capture_research.run_capture_experiment(path, self.config())
            self.assertEqual(reads, [1_048_577])
            self.assertEqual(report["source"]["sha256"], hashlib.sha256(raw).hexdigest())
            fifo = Path(temporary) / "fifo"
            os.mkfifo(fifo)
            for input_path in (fifo, Path(temporary)):
                for argument in ("--capture", "--config"):
                    command = [sys.executable, "-B", "-m", "omni_research.capture_cli", "--capture", str(path), "--config", str(ROOT / "contracts/fixtures/strategy-improvement-paper-config.json")]
                    command[command.index(argument) + 1] = str(input_path)
                    result = subprocess.run(command, env={**os.environ, "PYTHONPATH": str(ROOT / "services/research")}, capture_output=True, timeout=5)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(result.stdout, b"")

    def test_cli_rejects_extra_options_and_reports_transport_failure(self) -> None:
        from omni_research.capture_cli import main
        raw = json.dumps(synthetic_capture()).encode()
        for flag in ("--output", "--host", "--environment", "--credential", "--promotion", "--capt"):
            result = self.native(raw, extra=(flag, "unused"))
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, b"")
        with tempfile.TemporaryDirectory() as temporary:
            capture = Path(temporary) / "capture.json"
            capture.write_bytes(raw)
            with patch("sys.stdout.write", side_effect=OSError("transport unavailable")), patch("sys.stderr", new_callable=io.StringIO) as stderr:
                self.assertEqual(main(["--capture", str(capture), "--config", str(ROOT / "contracts/fixtures/strategy-improvement-paper-config.json")]), 1)
            self.assertEqual(stderr.getvalue(), "Capture research output failed.\n")

    def test_arbitrary_warmup_keeps_local_trade_labels_and_terminal_liquidation(self) -> None:
        from decimal import Decimal
        config = self.config()
        config["strategy"]["quantity"] = "4"
        config["execution"].update(starting_cash="1000", tax="0.01")
        bars = [Bar(f"local-{day}", "SMA", Decimal(100), Decimal(100), Decimal(100), Decimal(100), Decimal(10 if day == 8 else 5)) for day in range(1, 9)]
        for version in ("strategy-improvement.v1", "strategy-improvement.v2"):
            config["schema_version"] = version
            expected = simulate_fold(bars, parse_config(config), None)
            for count in (1, 3, 17, 90):
                warmup = [Bar(f"past-{index}", "SMA", Decimal(100), Decimal(100), Decimal(100), Decimal(100), Decimal(100)) for index in range(count)]
                self.assertEqual(simulate_fold(bars, parse_config(config), None, warmup=warmup), expected)
                self.assertEqual(expected[1][-1]["fill_at"], "local-8")
                with self.assertRaisesRegex(ValueError, "insufficient bars"):
                    simulate_fold(bars[:1], parse_config(config), Parameters(2, count + 3), warmup=warmup)

    def test_report_schema_closes_analysis_and_all_new_nested_objects(self) -> None:
        schema = json.loads((ROOT / "contracts/captured-candle-research.schema.json").read_text())
        report = self.research(synthetic_capture())
        def check(value, rule, document=schema):
            if "$ref" in rule:
                filename, _, fragment = rule["$ref"].partition("#")
                target = json.loads((ROOT / "contracts" / filename).read_text()) if filename else document
                resolved = target
                for key in fragment.strip("/").split("/") if fragment else []: resolved = resolved[key]
                check(value, resolved, target)
            for item in rule.get("allOf", []): check(value, item, document)
            if "const" in rule: self.assertEqual(value, rule["const"])
            if "enum" in rule: self.assertIn(value, rule["enum"])
            if "pattern" in rule: self.assertIsNotNone(re.fullmatch(rule["pattern"], value))
            if "maxLength" in rule: self.assertLessEqual(len(value), rule["maxLength"])
            if "minimum" in rule: self.assertGreaterEqual(value, rule["minimum"])
            if "maximum" in rule: self.assertLessEqual(value, rule["maximum"])
            if isinstance(value, dict):
                self.assertTrue(set(rule.get("required", [])) <= set(value))
                if rule.get("additionalProperties") is False: self.assertTrue(set(value) <= set(rule["properties"]))
                for key, subrule in rule.get("properties", {}).items():
                    if key in value: check(value[key], subrule, document)
            if isinstance(value, list):
                self.assertGreaterEqual(len(value), rule.get("minItems", 0))
                self.assertLessEqual(len(value), rule.get("maxItems", len(value)))
                if "items" in rule:
                    for item in value: check(item, rule["items"], document)
        check(report, schema)
        def closed(rule):
            if rule.get("type") == "object":
                self.assertIs(rule.get("additionalProperties"), False)
                self.assertEqual(set(rule["required"]), set(rule["properties"]))
            for value in rule.values():
                if isinstance(value, dict): closed(value)
                if isinstance(value, list):
                    for item in value:
                        if isinstance(item, dict): closed(item)
        closed(schema)
        for key in ("analysis", "limitations", "source"):
            changed = copy.deepcopy(report)
            changed[key]["unexpected"] = "x"
            with self.assertRaises(AssertionError): check(changed, schema)


if __name__ == "__main__":
    unittest.main()
