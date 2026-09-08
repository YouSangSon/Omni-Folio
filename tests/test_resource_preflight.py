"""Owning Make targets must reject unobservable process identity before side effects."""

import os
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest


MAKEFILE = Path(__file__).resolve().parents[1] / "Makefile"


class ProcessIdentityPreflightTest(unittest.TestCase):
    def test_unobservable_identity_never_starts_work_or_allocates_resources(self):
        targets = ("test", "check", "smoke", "test-resource-cleanup", "clean-test-resources")
        cases = (
            "command_denied", "start_denied", "start_error_output", "command_empty",
            "start_empty", "command_whitespace", "start_whitespace", "valid",
        )
        for target in targets:
            for case in cases:
                with self.subTest(target=target, case=case):
                    with tempfile.TemporaryDirectory(prefix="omni-folio-preflight.") as directory:
                        root = Path(directory)
                        (root / "Makefile").symlink_to(MAKEFILE)
                        (root / "services/research").mkdir(parents=True)
                        preserved = root / "apps/client/build/preserve"
                        preserved.parent.mkdir(parents=True)
                        preserved.write_text("uninspected artifact", encoding="utf-8")
                        binaries = root / "bin"
                        binaries.mkdir()
                        ps = binaries / "ps"
                        ps.write_text(
                            "#!/bin/sh\n"
                            'case "$OMNI_PREFLIGHT_CASE:$4" in\n'
                            "  command_denied:command=|start_denied:lstart=) exit 1;;\n"
                            "  command_empty:command=|start_empty:lstart=) exit 0;;\n"
                            "  start_error_output:lstart=) printf 'untrusted start\\n'; exit 1;;\n"
                            "  command_whitespace:command=|start_whitespace:lstart=) printf '  \\t  \\n'; exit 0;;\n"
                            "esac\n"
                            "printf 'fixture owner identity\\n'\n",
                            encoding="utf-8",
                        )
                        ps.chmod(0o700)
                        # These sentinels cannot create children, resources or runtime output.
                        # They also prevent an unfixed wrapper from running an actual test suite.
                        sentinel = binaries / "unexpected-work"
                        sentinel.write_text(
                            "#!/bin/sh\n"
                            'printf "%s\\n" "$0" >> "$OMNI_PREFLIGHT_ACTIONS"\n'
                            'printf "%s/omni-folio-test.never-created\\n" "$TMPDIR"\n'
                            "exit 77\n",
                            encoding="utf-8",
                        )
                        sentinel.chmod(0o700)
                        (binaries / "mktemp").symlink_to(sentinel)
                        actions = root / "unexpected-actions"
                        environment = {
                            **os.environ,
                            "PATH": f"{binaries}:{os.environ['PATH']}",
                            "TMPDIR": str(root),
                            "OMNI_PREFLIGHT_CASE": case,
                            "OMNI_PREFLIGHT_ACTIONS": str(actions),
                            "MAKEFLAGS": "",
                            "MFLAGS": "",
                        }
                        result = subprocess.run(
                            [
                                "/usr/bin/make", "--no-print-directory", "-f", str(MAKEFILE), target,
                                *[f"{name}={sentinel}" for name in ("GO", "GOFMT", "FLUTTER", "DART", "PYTHON")],
                            ],
                            cwd=root,
                            env=environment,
                            capture_output=True,
                            text=True,
                            timeout=15,
                            check=False,
                        )
                        if case == "valid":
                            # Positive control: a broken Make recipe or unconditional rejection
                            # cannot satisfy the fault matrix. Never launch real workloads.
                            if target == "clean-test-resources":
                                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                                self.assertFalse(preserved.exists())
                            else:
                                self.assertNotEqual(result.returncode, 0)
                                self.assertTrue(actions.exists(), result.stdout + result.stderr)
                                self.assertIn("/mktemp", actions.read_text(encoding="utf-8"))
                        else:
                            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                            self.assertIn("cannot verify test resource owner process identity", result.stderr)
                            self.assertFalse(actions.exists(), result.stdout + result.stderr)
                            self.assertTrue(preserved.exists(), "cleanup ran without process observation")
                            self.assertEqual(sorted(p.name for p in root.iterdir()), ["Makefile", "apps", "bin", "services"])


if __name__ == "__main__":
    def terminate(_signal, _frame):
        raise SystemExit(143)

    # TemporaryDirectory and subprocess handles unwind on normal test failure,
    # KeyboardInterrupt and TERM; full-suite hard-kill recovery is the Make owner.
    signal.signal(signal.SIGTERM, terminate)
    unittest.main()
