from __future__ import annotations

import copy
import hashlib
import json
import tempfile
import unittest
from decimal import Decimal, localcontext
from fractions import Fraction
from pathlib import Path
from unittest.mock import patch

import test_improve as fixtures
from omni_research.engine import Bar, canonical_hash
from omni_research.improve import parse_config, run_experiment, simulate_fold
from omni_research.improve_cli import main
from omni_research.paper_fill import calculate_fill, exact_decimal, ratio_string

ROOT = Path(__file__).parents[3]


class PaperImprovementTest(unittest.TestCase):
    def test_explicit_paper_model_is_separate_from_legacy_research(self) -> None:
        fixture = fixtures.ImprovementTest()
        config = fixture.config()
        config["schema_version"] = "strategy-improvement.v2"
        with tempfile.TemporaryDirectory() as temporary:
            bars = fixture.write_bars(Path(temporary))
            result = run_experiment(bars, config)
            legacy = run_experiment(bars, fixture.config())
        self.assertEqual(result["schema_version"], "strategy-improvement-result.v2")
        self.assertEqual(result["manifest"]["evaluation_policy"]["version"], "sma-paper-fill-walk-forward.v1")
        self.assertEqual(legacy["schema_version"], "strategy-improvement-result.v1")
        self.assertNotEqual(result["result_sha256"], legacy["result_sha256"])
        for trade in result["challenger"]["final_holdout_trades"]:
            self.assertRegex(trade["quantity"], r"^[1-9][0-9]*$")

    def test_actual_fill_matches_shared_go_contract(self) -> None:
        cases = json.loads((ROOT / "contracts/fixtures/paper-fill-cases.json").read_text())["cases"]
        for case in cases:
            with self.subTest(case=case["name"]):
                if case["error"]:
                    with self.assertRaises(ValueError):
                        calculate_fill(case["policy"], case["input"])
                else:
                    self.assertEqual(calculate_fill(case["policy"], case["input"]), case["expected"])

    def test_fold_carries_exact_cash_and_remaining_shares_between_partial_fills(self) -> None:
        raw = fixtures.ImprovementTest().config()
        raw["schema_version"] = "strategy-improvement.v2"
        raw["strategy"]["quantity"] = "4"
        raw["execution"].update(starting_cash="1000", tax="0.01")
        bars = [Bar(f"2026-01-{day:02d}T00:00:00Z", "SMA", Decimal(100), Decimal(100), Decimal(100), Decimal(100), Decimal(10 if day == 8 else 5)) for day in range(1, 9)]
        metrics, trades = simulate_fold(bars, parse_config(raw), None)
        self.assertEqual([(t["side"], t["quantity"], t["price"], t["fill_at"]) for t in trades], [
            ("BUY", "2", "100.1", "2026-01-02T00:00:00Z"),
            ("BUY", "2", "100.1", "2026-01-03T00:00:00Z"),
            ("SELL", "4", "99.9", "2026-01-08T00:00:00Z"),
        ])
        self.assertEqual(metrics["after_cost_return"], "-0.007796")  # Closing cash: 992.204.
        self.assertEqual(metrics["fees"], "3")
        self.assertEqual(metrics["taxes"], "3.996")
        self.assertEqual(metrics["slippage_cost"], "0.8")
        self.assertEqual(metrics["turnover"], "0.8")
        self.assertEqual(metrics["partial_fill_count"], "1")
        self.assertEqual(metrics["trade_count"], "1")
        self.assertEqual(metrics["fill_count"], "3")
        self.assertEqual(trades[0]["eligible_at"], trades[0]["fill_at"])
        self.assertEqual(trades[1]["eligible_at"], trades[0]["fill_at"])
        raw["execution"]["starting_cash"] = "401.39"
        with self.assertRaisesRegex(ValueError, "cannot complete every delayed"):
            simulate_fold(bars, parse_config(raw), None)

    def test_ratio_rounding_is_half_even_and_money_never_rounds(self) -> None:
        self.assertEqual(ratio_string(Fraction(1, 3)), "0.3333333333333333333333333333")
        self.assertEqual(ratio_string(Fraction(5, 10**29)), "0")
        self.assertEqual(ratio_string(Fraction(-5, 10**29)), "0")
        self.assertEqual(ratio_string(Fraction(15, 10**29)), "0.0000000000000000000000000002")
        self.assertEqual(exact_decimal(Fraction("12345678901234567890123456789.01")), "12345678901234567890123456789.01")
        with self.assertRaisesRegex(ValueError, "finite decimal"):
            exact_decimal(Fraction(1, 3))

    def test_new_model_rejects_fractional_or_out_of_bounds_strategy(self) -> None:
        base = fixtures.ImprovementTest().config()
        base["schema_version"] = "strategy-improvement.v2"
        for field, value in (("quantity", "0.5"), ("quantity", "4611686018427387904"), ("slow_windows", [366]), ("version", "2.0.0")):
            with self.subTest(field=field, value=value):
                config = copy.deepcopy(base)
                config["strategy"][field] = value
                with self.assertRaisesRegex(ValueError, "paper evaluation requires"):
                    parse_config(config)
        base["schema_version"] = "strategy-improvement.v1"
        base["strategy"]["quantity"] = "0.5"
        self.assertEqual(parse_config(base).quantity, Decimal("0.5"))
        for invalid_version in ([], {}, None, 2):
            with self.subTest(schema_version=invalid_version):
                base["schema_version"] = invalid_version
                with self.assertRaisesRegex(ValueError, "config must be"):
                    parse_config(base)

    def test_new_model_rejects_unsupported_sma_price_precision(self) -> None:
        fixture = fixtures.ImprovementTest()
        config = fixture.config()
        config["schema_version"] = "strategy-improvement.v2"
        with tempfile.TemporaryDirectory() as temporary:
            bars = fixture.write_bars(Path(temporary))
            original = bars.read_text()
            for old, new in ((",20,21,19,20,", ",20,21,19,20.000000001,"), (",20,21,19,20,", ",1000000000000,1000000000000,1000000000000,1000000000000,")):
                bars.write_text(original.replace(old, new, 1))
                with self.assertRaisesRegex(ValueError, "paper SMA closes"):
                    run_experiment(bars, config)

    def test_holdout_does_not_choose_parameters_and_decimal_context_cannot_change_v2(self) -> None:
        fixture = fixtures.ImprovementTest()
        config = fixture.config()
        config["schema_version"] = "strategy-improvement.v2"
        with tempfile.TemporaryDirectory() as temporary:
            bars = fixture.write_bars(Path(temporary))
            ordinary = run_experiment(bars, config)
            with localcontext() as context:
                context.prec = 6
                self.assertEqual(run_experiment(bars, config), ordinary)
            changed = run_experiment(fixture.write_bars(Path(temporary), altered_holdout=True), config)
        self.assertEqual(ordinary["candidates"], changed["candidates"])
        self.assertEqual(ordinary["challenger"]["walk_forward_folds"], changed["challenger"]["walk_forward_folds"])
        self.assertNotEqual(ordinary["challenger"]["final_holdout_metrics"], changed["challenger"]["final_holdout_metrics"])

    def test_manifest_hashes_the_same_single_capture_that_was_parsed(self) -> None:
        fixture = fixtures.ImprovementTest()
        with tempfile.TemporaryDirectory() as temporary:
            bars = fixture.write_bars(Path(temporary))
            captured = bars.read_bytes()
            for version in ("strategy-improvement.v1", "strategy-improvement.v2"):
                config = fixture.config()
                config["schema_version"] = version
                # A second read would observe a replacement and must not occur.
                with patch.object(Path, "read_bytes", side_effect=[captured, b"replaced"]) as read:
                    result = run_experiment(bars, config)
                self.assertEqual(read.call_count, 1)
                self.assertEqual(result["input_sha256"], hashlib.sha256(captured).hexdigest())
                self.assertEqual(result["result_sha256"], canonical_hash({k: v for k, v in result.items() if k != "result_sha256"}))

    def test_existing_cli_emits_explicit_v2_fixture_and_canonical_result(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "result.json"
            self.assertEqual(main(["--bars", str(ROOT / "contracts/fixtures/strategy-market-bars.csv"), "--config", str(ROOT / "contracts/fixtures/strategy-improvement-paper-config.json"), "--output", str(output)]), 0)
            result = json.loads(output.read_text())
        self.assertEqual(result["schema_version"], "strategy-improvement-result.v2")
        self.assertEqual(result["result_sha256"], canonical_hash({k: v for k, v in result.items() if k != "result_sha256"}))
