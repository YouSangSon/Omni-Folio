from __future__ import annotations

import unittest
from pathlib import Path


class ResearchBoundaryTest(unittest.TestCase):
    def test_package_has_no_network_credential_or_operational_database_surface(self) -> None:
        package = Path(__file__).parents[1] / "omni_research"
        source = "\n".join(path.read_text(encoding="utf-8") for path in package.glob("*.py"))
        for forbidden in ("os.environ", "socket", "urllib", "http.client", "sqlite3", "subprocess"):
            self.assertNotIn(forbidden, source)


if __name__ == "__main__":
    unittest.main()
