from __future__ import annotations

import unittest
from decimal import Decimal

from omni_research.improve import Parameters, sma_crossover


class SMACrossoverTest(unittest.TestCase):
    parameters = Parameters(fast_window=2, slow_window=3)

    def test_classifies_crossovers_and_insufficient_history(self) -> None:
        cases = (
            (["3", "2", "1", "4"], 3, "golden_cross"),
            (["1", "2", "3", "0"], 3, "death_cross"),
            (["1", "1", "1", "1"], 3, "none"),
            (["2", "1", "3", "4"], 3, "golden_cross"),
            (["2", "3", "1", "1"], 3, "death_cross"),
            (["3", "2", "1"], 2, "none"),
        )
        for raw, end, expected in cases:
            with self.subTest(raw=raw, end=end):
                self.assertEqual(sma_crossover([Decimal(value) for value in raw], end, self.parameters), expected)

    def test_is_invariant_to_future_suffix(self) -> None:
        prefix = [Decimal(value) for value in ["3", "2", "1", "4"]]
        self.assertEqual(sma_crossover(prefix, 3, self.parameters), sma_crossover(prefix + [Decimal("99"), Decimal("1")], 3, self.parameters))


if __name__ == "__main__":
    unittest.main()
