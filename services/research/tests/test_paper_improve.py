from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import test_improve as fixtures
from omni_research.improve import run_experiment


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
