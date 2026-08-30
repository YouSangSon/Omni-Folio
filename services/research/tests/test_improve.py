from __future__ import annotations

import csv
import json
import re
import tempfile
import unittest
from datetime import date, timedelta
from pathlib import Path

from omni_research.engine import canonical_hash
from omni_research.improve import run_experiment
from omni_research.improve_cli import ALLOWED_ARGUMENTS, main, parser

ROOT = Path(__file__).parents[3]


class ImprovementTest(unittest.TestCase):
    def config(self) -> dict[str, object]:
        return {
            "schema_version": "strategy-improvement.v1",
            "experiment_id": "sma-grid-fixture-001",
            "data": {"version": "sma-fixture-v1"},
            "strategy": {
                "name": "long_only_sma_crossover",
                "version": "1.0.0",
                "quantity": "10",
                "fast_windows": [2, 3],
                "slow_windows": [5, 6],
            },
            "execution": {
                "starting_cash": "10000",
                "fee": "1",
                "tax": "0.001",
                "slippage_bps": "10",
                "delay_bars": "1",
                "max_participation": "0.5",
                "signal_price": "bar_close",
                "fill_price": "next_eligible_bar_open",
            },
            "splits": {"train_bars": 30, "validation_bars": 60, "holdout_bars": 30, "minimum_fold_bars": 30},
            "promotion": {
                "baseline": "buy_and_hold",
                "minimum_validation_after_cost_return": "-1",
                "minimum_validation_trade_count": 1,
                "minimum_holdout_after_cost_return": "-1",
                "maximum_holdout_max_drawdown": "1",
                "minimum_holdout_trade_count": 1,
            },
        }

    def write_bars(self, directory: Path, *, altered_holdout: bool = False, baseline_wins: bool = False, segment_final_volume: str = "100") -> Path:
        rising = [10, 9, 8, 7, 8, 9, *range(10, 34)] if baseline_wins else [*range(20, 8, -1), *range(10, 28)]
        holdout = list(reversed(rising)) if altered_holdout else rising
        values = rising + rising + rising + holdout
        bars = directory / "bars.csv"
        with bars.open("w", encoding="utf-8", newline="") as target:
            writer = csv.DictWriter(target, fieldnames=["bar_at", "symbol", "open", "high", "low", "close", "volume"])
            writer.writeheader()
            first = date(2026, 1, 1)
            for index, value in enumerate(values):
                writer.writerow({
                    "bar_at": f"{first + timedelta(days=index)}T00:00:00Z",
                    "symbol": "SMA",
                    "open": str(value),
                    "high": str(value + 1),
                    "low": str(value - 1),
                    "close": str(value),
                    "volume": segment_final_volume if (index + 1) % 30 == 0 else "100",
                })
        return bars

    def test_result_is_deterministic_hashable_and_uses_next_eligible_bar(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars = self.write_bars(Path(temporary))
            first = run_experiment(bars, self.config())
            second = run_experiment(bars, self.config())
        self.assertEqual(first, second)
        self.assertEqual(first["result_sha256"], canonical_hash({key: value for key, value in first.items() if key != "result_sha256"}))
        self.assertEqual(first["manifest"]["data"]["version"], "sma-fixture-v1")
        self.assertEqual(first["manifest"]["engine"]["version"], "0.1.0")
        self.assertEqual(
            first["manifest"]["strategy"]["parameter_hash"],
            canonical_hash(first["manifest"]["strategy"]["parameters"]),
        )
        self.assertEqual(first["manifest"]["evaluation_policy"]["version"], "sma-expanding-walk-forward.v1")
        self.assertEqual(first["evaluation"]["method"], "expanding_walk_forward_then_final_holdout")
        folds = first["challenger"]["walk_forward_folds"]
        self.assertEqual(len(folds), 2)
        self.assertEqual([fold["train"]["bar_count"] for fold in folds], [30, 60])
        self.assertEqual([fold["validation"]["bar_count"] for fold in folds], [30, 30])
        self.assertEqual(first["evaluation"]["final_holdout"]["bar_count"], 30)
        result_schema = json.loads((ROOT / "contracts" / "strategy-improvement-result.schema.json").read_text(encoding="utf-8"))
        config_schema = json.loads((ROOT / "contracts" / "strategy-improvement-config.schema.json").read_text(encoding="utf-8"))
        self.assertEqual(result_schema["properties"]["schema_version"]["const"], first["schema_version"])
        self.assertFalse(result_schema["additionalProperties"])
        self.assertIn("execution", result_schema["required"])
        execution_schema = result_schema["properties"]["execution"]
        self.assertFalse(execution_schema["additionalProperties"])
        self.assertEqual(
            set(execution_schema["required"]),
            {"starting_cash", "fee", "tax", "slippage_bps", "delay_bars", "max_participation", "signal_price", "fill_price"},
        )
        self.assertEqual(execution_schema["properties"]["signal_price"]["const"], "bar_close")
        self.assertEqual(execution_schema["properties"]["fill_price"]["const"], "next_eligible_bar_open")
        self.assertFalse(result_schema["properties"]["manifest"]["additionalProperties"])
        self.assertFalse(result_schema["properties"]["promotion"]["additionalProperties"])
        self.assertEqual(
            result_schema["properties"]["manifest"]["properties"]["evaluation_policy"]["properties"]["version"]["const"],
            first["manifest"]["evaluation_policy"]["version"],
        )
        self.assertEqual(config_schema["properties"]["schema_version"]["const"], self.config()["schema_version"])
        self.assertTrue(first["challenger"]["final_holdout_trades"])
        for trade in first["challenger"]["final_holdout_trades"]:
            self.assertLess(trade["signal_at"], trade["eligible_at"])
            self.assertLessEqual(trade["eligible_at"], trade["fill_at"])
            self.assertEqual(
                date.fromisoformat(trade["eligible_at"][:10]),
                date.fromisoformat(trade["signal_at"][:10]) + timedelta(days=1),
            )
        self.assertEqual(first["challenger"]["final_holdout_metrics"]["lookahead_violations"], "0")

    def test_holdout_is_not_used_to_select_the_challenger(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            ordinary = run_experiment(self.write_bars(directory), self.config())
            changed = run_experiment(self.write_bars(directory, altered_holdout=True), self.config())
        self.assertEqual(ordinary["candidates"], changed["candidates"])
        self.assertEqual(ordinary["challenger"]["parameters"], changed["challenger"]["parameters"])
        self.assertEqual(ordinary["challenger"]["walk_forward_folds"], changed["challenger"]["walk_forward_folds"])
        self.assertNotEqual(ordinary["challenger"]["final_holdout_metrics"], changed["challenger"]["final_holdout_metrics"])

    def test_promotion_requires_validation_holdout_and_baseline_gates(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars = self.write_bars(Path(temporary))
            candidate = run_experiment(bars, self.config())
            blocked_config = self.config()
            blocked_config["promotion"] = {**blocked_config["promotion"], "minimum_holdout_after_cost_return": "999"}  # type: ignore[index]
            blocked = run_experiment(bars, blocked_config)
            walk_forward_config = self.config()
            walk_forward_config["promotion"] = {**walk_forward_config["promotion"], "minimum_validation_after_cost_return": "999"}  # type: ignore[index]
            walk_forward_blocked = run_experiment(bars, walk_forward_config)
        self.assertEqual(candidate["promotion"]["target"], "paper_candidate")
        self.assertEqual(blocked["promotion"]["target"], "no_promotion")
        self.assertIn("final_holdout", blocked["promotion"]["failed_gates"])
        self.assertEqual(walk_forward_blocked["promotion"]["target"], "no_promotion")
        self.assertFalse(walk_forward_blocked["promotion"]["walk_forward_gate_passed"])
        self.assertIn("walk_forward", walk_forward_blocked["promotion"]["failed_gates"])
        self.assertIn(candidate["promotion"]["target"], {"paper_candidate", "no_promotion"})

    def test_buy_and_hold_baseline_blocks_a_best_of_bad_challenger(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = run_experiment(self.write_bars(Path(temporary), baseline_wins=True), self.config())
        self.assertEqual(result["promotion"]["target"], "no_promotion")
        self.assertFalse(result["promotion"]["baseline_gate_passed"])
        self.assertIn("buy_and_hold_baseline", result["promotion"]["failed_gates"])

    def test_partial_fills_count_as_fills_not_completed_round_trips(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars = self.write_bars(Path(temporary), segment_final_volume="200")
            config = self.config()
            config["strategy"] = {**config["strategy"], "quantity": "60"}  # type: ignore[index]
            result = run_experiment(bars, config)
        metrics = result["challenger"]["final_holdout_metrics"]
        self.assertGreater(int(metrics["partial_fill_count"]), 0)
        self.assertGreater(int(metrics["fill_count"]), int(metrics["trade_count"]))
        self.assertGreaterEqual(int(metrics["trade_count"]), 1)

    def test_fail_closed_for_short_folds_and_zero_delay(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars = self.write_bars(Path(temporary))
            short = self.config()
            short["splits"] = {"train_bars": 29, "validation_bars": 60, "holdout_bars": 31, "minimum_fold_bars": 29}  # type: ignore[index]
            with self.assertRaisesRegex(ValueError, "at least 30"):
                run_experiment(bars, short)
            one_fold = self.config()
            one_fold["splits"] = {"train_bars": 30, "validation_bars": 30, "holdout_bars": 60, "minimum_fold_bars": 30}  # type: ignore[index]
            with self.assertRaisesRegex(ValueError, "walk-forward folds"):
                run_experiment(bars, one_fold)
            zero_delay = self.config()
            zero_delay["execution"] = {**zero_delay["execution"], "delay_bars": "0"}  # type: ignore[index]
            with self.assertRaisesRegex(ValueError, "prevent lookahead"):
                run_experiment(bars, zero_delay)
            no_trade_gate = self.config()
            no_trade_gate["promotion"] = {**no_trade_gate["promotion"], "minimum_validation_trade_count": 0}  # type: ignore[index]
            with self.assertRaisesRegex(ValueError, "at least 1"):
                run_experiment(bars, no_trade_gate)

    def test_execution_costs_reject_negative_zero(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars = self.write_bars(Path(temporary))
            for field in ("fee", "tax", "slippage_bps"):
                with self.subTest(field=field):
                    config = self.config()
                    config["execution"] = {**config["execution"], field: "-0"}  # type: ignore[index]
                    with self.assertRaisesRegex(ValueError, "canonical decimal string"):
                        run_experiment(bars, config)

    def test_result_schema_execution_ranges_match_runtime(self) -> None:
        schema = json.loads((ROOT / "contracts" / "strategy-improvement-result.schema.json").read_text(encoding="utf-8"))
        execution = schema["properties"]["execution"]["properties"]
        cases = {
            "starting_cash": (["1", "0.01"], ["0", "-1", "01", "1.0"]),
            "fee": (["0", "1", "0.01"], ["-0", "-1", "01", "1.0"]),
            "tax": (["0", "0.001"], ["-0", "-0.1", "00"]),
            "slippage_bps": (["0", "10", "0.5"], ["-0", "-1", "10.0"]),
            "delay_bars": (["1", "10"], ["0", "-1", "1.5", "01"]),
            "max_participation": (["1", "0.5", "0.001"], ["0", "-0", "-1", "1.1", "2"]),
        }
        for field, (accepted, rejected) in cases.items():
            pattern = schema["$defs"][execution[field]["$ref"].rsplit("/", 1)[1]]["pattern"]
            for value in accepted:
                self.assertIsNotNone(re.fullmatch(pattern, value), f"{field} rejected {value}")
            for value in rejected:
                self.assertIsNone(re.fullmatch(pattern, value), f"{field} admitted {value}")

    def test_cli_writes_canonical_result_and_only_accepts_local_inputs(self) -> None:
        self.assertEqual({action.dest for action in parser()._actions if action.dest != "help"}, ALLOWED_ARGUMENTS)
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars = self.write_bars(directory)
            config = directory / "config.json"
            output = directory / "result.json"
            config.write_text(json.dumps(self.config()), encoding="utf-8")
            self.assertEqual(main(["--bars", str(bars), "--config", str(config), "--output", str(output)]), 0)
            expected = run_experiment(bars, self.config())
            self.assertEqual(output.read_text(encoding="utf-8"), json.dumps(expected, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n")


if __name__ == "__main__":
    unittest.main()
