"""CLI for deterministic, offline SMA grid experiments."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Sequence

from .improve import run_experiment

ALLOWED_ARGUMENTS = frozenset({"bars", "config", "output"})


def parser() -> argparse.ArgumentParser:
    argument_parser = argparse.ArgumentParser(description="Evaluate an offline SMA parameter grid.")
    argument_parser.add_argument("--bars", required=True, type=Path)
    argument_parser.add_argument("--config", required=True, type=Path)
    argument_parser.add_argument("--output", type=Path)
    return argument_parser


def main(argv: Sequence[str] | None = None) -> int:
    args = parser().parse_args(argv)
    result = run_experiment(args.bars, json.loads(args.config.read_text(encoding="utf-8")))
    rendered = json.dumps(result, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    if args.output:
        args.output.write_text(rendered + "\n", encoding="utf-8")
    else:
        print(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
