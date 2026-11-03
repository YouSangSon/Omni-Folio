from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from omni_research.cli import ALLOWED_ARGUMENTS, main, parser
from omni_research.engine import DISCLAIMER, build_manifest

ROOT = Path(__file__).parents[3]
FIXTURES = ROOT / "contracts" / "fixtures"
RUN_ARGS = {
    "run_id": "run_buy_hold_fixture_001",
    "dataset_id": "dataset_market_bars_fixture",
    "started_at": "2026-08-23T06:00:00Z",
    "finished_at": "2026-08-23T06:00:00.010Z",
}


class BacktestTest(unittest.TestCase):
    def request(self) -> dict[str, object]:
        return json.loads((FIXTURES / "backtest-request.json").read_text(encoding="utf-8"))

    def test_golden_fixture_is_exact_and_deterministic(self) -> None:
        expected = json.loads((FIXTURES / "golden-backtest.json").read_text(encoding="utf-8"))
        bars = FIXTURES / "market-bars.csv"
        first = build_manifest(bars, self.request(), **RUN_ARGS)
        second = build_manifest(bars, self.request(), **RUN_ARGS)
        self.assertEqual(first, expected)
        self.assertEqual(second, expected)
        self.assertEqual(first["manifest"]["strategy"]["parameter_hash"], "3fe0ba7b698b6866a62055f39a3829ca3166588e7f6f2dcf39bbff4648e0ec71")
        for order in first["result"]["orders"]:
            self.assertLess(order["signal_at"], order["first_eligible_at"])
            self.assertEqual(order["first_eligible_at"], order["first_fill_at"])
        self.assertEqual(first["result"]["metrics"]["lookahead_violations"], "0")
        self.assertEqual(first["disclaimer"], DISCLAIMER)

    def test_cli_writes_canonical_golden_json(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "manifest.json"
            arguments = [
                "--bars", str(FIXTURES / "market-bars.csv"),
                "--request", str(FIXTURES / "backtest-request.json"),
                "--run-id", RUN_ARGS["run_id"],
                "--dataset-id", RUN_ARGS["dataset_id"],
                "--started-at", RUN_ARGS["started_at"],
                "--finished-at", RUN_ARGS["finished_at"],
                "--output", str(output),
            ]
            self.assertEqual(main(arguments), 0)
            expected = build_manifest(FIXTURES / "market-bars.csv", self.request(), **RUN_ARGS)
            self.assertEqual(output.read_text(encoding="utf-8"), json.dumps(expected, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n")

    def test_delay_zero_is_rejected_to_prevent_lookahead(self) -> None:
        request = self.request()
        request["execution"] = {**request["execution"], "delay_bars": "0"}  # type: ignore[index]
        with self.assertRaisesRegex(ValueError, "prevent lookahead"):
            build_manifest(FIXTURES / "market-bars.csv", request, **RUN_ARGS)

    def test_execution_rejects_tax_above_one_and_full_slippage(self) -> None:
        for field, value in (("tax", "1.0001"), ("slippage_bps", "10000")):
            with self.subTest(field=field):
                request = self.request()
                request["execution"] = {**request["execution"], field: value}  # type: ignore[index]
                with self.assertRaisesRegex(ValueError, "out of range"):
                    build_manifest(FIXTURES / "market-bars.csv", request, **RUN_ARGS)

    def test_invalid_capital_time_and_ohlc_are_rejected(self) -> None:
        request = self.request()
        request["execution"] = {**request["execution"], "starting_cash": "0"}  # type: ignore[index]
        with self.assertRaisesRegex(ValueError, "out of range"):
            build_manifest(FIXTURES / "market-bars.csv", request, **RUN_ARGS)

        with self.assertRaisesRegex(ValueError, "must not precede"):
            build_manifest(
                FIXTURES / "market-bars.csv",
                self.request(),
                **{**RUN_ARGS, "finished_at": "2026-08-23T05:59:59Z"},
            )

        with tempfile.TemporaryDirectory() as temporary:
            bars = Path(temporary) / "bars.csv"
            bars.write_text(
                "bar_at,symbol,open,high,low,close,volume\n"
                "2026-01-02T00:00:00Z,AAPL,10,9,8,10,100\n"
                "2026-01-03T00:00:00Z,AAPL,11,12,10,11,100\n",
                encoding="utf-8",
            )
            request = self.request()
            import hashlib

            request["data"] = {  # type: ignore[index]
                **request["data"],  # type: ignore[index]
                "input_sha256": hashlib.sha256(bars.read_bytes()).hexdigest(),
            }
            with self.assertRaisesRegex(ValueError, "valid positive OHLC"):
                build_manifest(bars, request, **RUN_ARGS)

    def test_config_surface_is_only_local_files_and_injected_identity(self) -> None:
        self.assertEqual({action.dest for action in parser()._actions if action.dest != "help"}, ALLOWED_ARGUMENTS)


if __name__ == "__main__":
    unittest.main()
