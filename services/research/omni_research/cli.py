"""Offline command line entrypoint; it has no credentials or order-submit path."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Sequence

from .engine import build_manifest

ALLOWED_ARGUMENTS = frozenset({"bars", "request", "run_id", "dataset_id", "started_at", "finished_at", "output"})


def parser() -> argparse.ArgumentParser:
    argument_parser = argparse.ArgumentParser(
        description="Build a deterministic, non-advice backtest manifest."
    )
    argument_parser.add_argument("--bars", required=True, type=Path)
    argument_parser.add_argument("--request", required=True, type=Path)
    argument_parser.add_argument("--run-id", required=True)
    argument_parser.add_argument("--dataset-id", required=True)
    argument_parser.add_argument("--started-at", required=True)
    argument_parser.add_argument("--finished-at", required=True)
    argument_parser.add_argument("--output", type=Path)
    return argument_parser


def main(argv: Sequence[str] | None = None) -> int:
    args = parser().parse_args(argv)
    request = json.loads(args.request.read_text(encoding="utf-8"))
    manifest = build_manifest(
        args.bars,
        request,
        run_id=args.run_id,
        dataset_id=args.dataset_id,
        started_at=args.started_at,
        finished_at=args.finished_at,
    )
    rendered = json.dumps(manifest, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    if args.output:
        args.output.write_text(rendered + "\n", encoding="utf-8")
    else:
        print(rendered)
    return 0
