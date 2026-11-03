"""Emit one offline capture research report to stdout."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Sequence

from .capture_research import _decode, _read_input, run_capture_experiment


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Research native daily captured observations without execution authority.", allow_abbrev=False)
    parser.add_argument("--capture", required=True, type=Path)
    parser.add_argument("--config", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        report = run_capture_experiment(args.capture, _decode(_read_input(args.config)))
        rendered = json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    except (OSError, ValueError, TypeError, KeyError, ArithmeticError, RecursionError):
        print("Capture research input or report is invalid; no report was emitted.", file=sys.stderr)
        return 1
    try:
        print(rendered, flush=True)
        return 0
    except BrokenPipeError:
        with open(os.devnull, "wb") as sink:
            os.dup2(sink.fileno(), sys.stdout.fileno())
        print("Capture research output failed.", file=sys.stderr)
        return 1
    except (OSError, ValueError):
        print("Capture research output failed.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
