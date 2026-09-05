"""Offline paper target proposals; never an order or an execution authorization."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import re
import stat
import sys
import time
from pathlib import Path
from typing import Any, Sequence

from .engine import canonical_hash, decimal_input, parse_bars, request_execution, timestamp
from .improve import Parameters, integer, sma_crossover

ARTIFACT_FIELDS = {
    "schema_version", "experiment_id", "input_sha256", "config_sha256", "manifest", "execution",
    "evaluation", "candidates", "challenger", "promotion", "disclaimer", "result_sha256",
}


def read_signal_input(path: Path) -> bytes:
    # Check the opened descriptor, not a racy path precheck. The byte cap is
    # not a general filesystem I/O deadline; O_NONBLOCK prevents FIFO waits.
    with open(path, 'rb', opener=lambda name, flags: os.open(name, flags | os.O_NONBLOCK)) as source:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise ValueError("signal input must be a regular file")
        raw = source.read(1_048_577)
        if len(raw) > 1_048_576:
            raise ValueError("signal input exceeds 1 MiB")
        return raw


def generate_proposal(bars_path: Path, research_bars_path: Path, artifact: dict[str, Any]) -> dict[str, Any]:
    # This validates the calculation inputs, not the complete research proof.
    # Self-hashes and claimed promotion gates remain untrusted until Go admission.
    if not isinstance(artifact, dict) or set(artifact) != ARTIFACT_FIELDS:
        raise ValueError("research artifact fields are invalid")
    body = {key: value for key, value in artifact.items() if key != "result_sha256"}
    if artifact["schema_version"] != "strategy-improvement-result.v1" or canonical_hash(body) != artifact["result_sha256"]:
        raise ValueError("research artifact hash is invalid")
    manifest = artifact["manifest"]
    strategy = manifest["strategy"]
    parameters = strategy["parameters"]
    if (set(manifest) != {"strategy", "data", "engine", "evaluation_policy"}
        or set(strategy) != {"name", "version", "parameters", "parameter_hash"}
        or strategy["name"] != "long_only_sma_crossover" or strategy["version"] != "1.0.0"
        or manifest["engine"] != {"name": "omni-folio-reference", "version": "0.1.0"}
        or manifest["evaluation_policy"] != {"version": "sma-expanding-walk-forward.v1"}
        or set(parameters) != {"quantity", "fast_window", "slow_window"}
        or canonical_hash(parameters) != strategy["parameter_hash"]):
        raise ValueError("unsupported strategy contract")
    request_execution({"execution": artifact["execution"]})
    promotion = artifact["promotion"]
    if (promotion["target"] != "paper_candidate" or promotion["failed_gates"] != []
        or any(promotion[key] is not True for key in ("walk_forward_gate_passed", "final_holdout_gate_passed", "baseline_gate_passed"))):
        raise ValueError("research candidate did not pass its paper gates")
    fast = integer(parameters["fast_window"], "fast_window", minimum=1)
    slow = integer(parameters["slow_window"], "slow_window", minimum=1)
    if fast >= slow or artifact["challenger"]["parameters"] != {"fast_window": fast, "slow_window": slow}:
        raise ValueError("strategy windows are invalid")
    quantity = parameters["quantity"]
    if not isinstance(quantity, str) or not re.fullmatch(r"[1-9][0-9]{0,63}", quantity):
        raise ValueError("paper quantity must be positive whole shares")
    decimal_input(quantity, "quantity")

    # Read each snapshot once: the hash and parsed bars must refer to the same bytes.
    research_bytes = read_signal_input(research_bars_path)
    research_sha = hashlib.sha256(research_bytes).hexdigest()
    if artifact["input_sha256"] != research_sha or manifest["data"]["input_sha256"] != research_sha:
        raise ValueError("research data hash mismatch")
    research_bars = parse_bars(research_bytes)
    raw_bars = read_signal_input(bars_path)
    bars = parse_bars(raw_bars)
    if len(bars) <= slow or not re.fullmatch(r"[0-9]{6}", bars[-1].symbol) or bars[-1].symbol != research_bars[-1].symbol:
        raise ValueError("paper signal requires matching KRX symbols and sufficient history")
    for bar in [*research_bars, *bars]:
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", bar.at):
            raise ValueError("daily signal timestamps must be UTC whole seconds")
    if timestamp(bars[-1].at, "signal time") <= timestamp(research_bars[-1].at, "research end"):
        raise ValueError("signal must follow the research sample")
    signal = sma_crossover([bar.close for bar in bars], len(bars) - 1, Parameters(fast, slow))
    proposal = {
        "schema_version": "paper-signal-proposal.v1", "mode": "paper_proposal_only",
        "strategy_result_sha256": artifact["result_sha256"], "strategy_parameter_sha256": strategy["parameter_hash"],
        "input_sha256": hashlib.sha256(raw_bars).hexdigest(), "symbol": bars[-1].symbol,
        "data_as_of": bars[-1].at, "signal": signal,
        "target_quantity": quantity if signal == "golden_cross" else "0" if signal == "death_cross" else None,
    }
    return {**proposal, "proposal_sha256": canonical_hash(proposal)}


def _object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def _invalid_number(_: str) -> None:
    raise ValueError("non-integer JSON number")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Produce a local SMA paper target proposal without execution authority.")
    parser.add_argument("--bars", required=True, type=Path)
    parser.add_argument("--research-bars", required=True, type=Path)
    parser.add_argument("--artifact", required=True, type=Path)
    parser.add_argument("--watch", action="store_true", help="emit changed latest-close proposals as NDJSON; no execution authority")
    args = parser.parse_args(argv)
    try:
        raw = read_signal_input(args.artifact)
        artifact = json.loads(raw, object_pairs_hook=_object, parse_float=_invalid_number, parse_constant=_invalid_number)
        previous = None
        # ponytail: latest-snapshot polling can miss intermediate closes;
        # durable exact-byte delivery belongs in the later Go ingestion path.
        while True:
            if previous is not None and read_signal_input(args.artifact) != raw:
                raise ValueError("watch research artifact changed")
            proposal = generate_proposal(args.bars, args.research_bars, artifact)
            if proposal != previous:
                if previous is not None and proposal["data_as_of"] <= previous["data_as_of"]:
                    raise ValueError("watch requires a new close, not rewritten history")
                print(json.dumps(proposal, ensure_ascii=False, sort_keys=True, separators=(",", ":")), flush=True)
                previous = proposal
            if not args.watch:
                return 0
            time.sleep(1)
    except KeyboardInterrupt:
        return 130
    except BrokenPipeError:
        # Python flushes stdout again at shutdown. Redirect only this broken
        # output descriptor so termination cannot emit a second traceback.
        with open(os.devnull, 'wb') as sink:
            os.dup2(sink.fileno(), sys.stdout.fileno())
        print("Paper signal proposal output is closed; producer stopped.", file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, TypeError, ArithmeticError, RecursionError, csv.Error):
        print("Paper signal proposal input is invalid; no proposal was produced.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
