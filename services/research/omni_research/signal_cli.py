"""Offline paper target proposals; never an order or an execution authorization."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import re
import secrets
import select
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


def generate_proposal_from_bytes(raw_bars: bytes, research_bytes: bytes, artifact: dict[str, Any]) -> dict[str, Any]:
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

    # Hash and parsed bars must refer to the same immutable byte snapshots.
    research_sha = hashlib.sha256(research_bytes).hexdigest()
    if artifact["input_sha256"] != research_sha or manifest["data"]["input_sha256"] != research_sha:
        raise ValueError("research data hash mismatch")
    research_bars = parse_bars(research_bytes)
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


def generate_proposal(bars_path: Path, research_bars_path: Path, artifact: dict[str, Any]) -> dict[str, Any]:
    # Keep the path API for callers while binding calculation to single reads.
    research_bytes = read_signal_input(research_bars_path)
    raw_bars = read_signal_input(bars_path)
    return generate_proposal_from_bytes(raw_bars, research_bytes, artifact)


def _json(value: dict[str, Any]) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def _raise_if_stdout_pipe_closed() -> None:
    try:
        fd = sys.stdout.fileno()
        if not stat.S_ISFIFO(os.fstat(fd).st_mode):
            return
    except (AttributeError, OSError, ValueError):
        return
    poller = select.poll()
    poller.register(fd, select.POLLHUP | select.POLLERR)
    if any(event & (select.POLLHUP | select.POLLERR) for _, event in poller.poll(0)):
        raise BrokenPipeError


def generate_paper_input_bundle(proposal: dict[str, Any], raw_bars: bytes, research_bytes: bytes) -> dict[str, Any]:
    if len(raw_bars) > 1_048_576 or len(research_bytes) > 1_048_576:
        raise ValueError("bundle CSV input exceeds 1 MiB")
    try:
        bars_csv = raw_bars.decode("utf-8")
        research_csv = research_bytes.decode("utf-8")
    except UnicodeDecodeError as error:
        raise ValueError("bundle CSV must be UTF-8") from error
    bundle = {
        "schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only", "proposal": proposal,
        "bars_csv": bars_csv, "research_csv": research_csv,
    }
    # JSON escaping can expand valid one-MiB snapshots beyond the Go ingress cap.
    if len(_json(bundle).encode("utf-8")) + 1 > 4 * 1_048_576:
        raise ValueError("bundle exceeds 4 MiB")
    return bundle


def _object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def _invalid_number(_: str) -> None:
    raise ValueError("non-integer JSON number")


def _capture_bundle(directory: Path, name: str, raw: bytes) -> None:
    # Pin the directory so path replacement cannot redirect publication/cleanup.
    directory_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    temporary = None
    try:
        info = os.fstat(directory_fd)
        if info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError("capture directory must be private and owned")
        candidate = '.paper-capture-' + secrets.token_hex(16) + '.tmp'
        fd = os.open(candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=directory_fd)
        temporary = candidate
        with os.fdopen(fd, 'wb') as target:
            target.write(raw)
            target.flush()
            os.fsync(target.fileno())
        try:
            os.link(temporary, name, src_dir_fd=directory_fd, dst_dir_fd=directory_fd, follow_symlinks=False)
        except FileExistsError:
            fd = os.open(name, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW, dir_fd=directory_fd)
            with os.fdopen(fd, 'rb') as existing:
                info = os.fstat(existing.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_size > 4 * 1_048_576:
                    raise ValueError("capture destination is not a bounded regular file")
                if existing.read(4 * 1_048_576 + 1) != raw:
                    raise ValueError("capture destination differs")
        os.unlink(temporary, dir_fd=directory_fd)
        temporary = None
        os.fsync(directory_fd)
    finally:
        try:
            if temporary is not None:
                os.unlink(temporary, dir_fd=directory_fd)
        finally:
            os.close(directory_fd)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Produce a local SMA paper target proposal without execution authority.")
    parser.add_argument("--bars", required=True, type=Path)
    parser.add_argument("--research-bars", required=True, type=Path)
    parser.add_argument("--artifact", required=True, type=Path)
    parser.add_argument("--watch", action="store_true", help="emit changed latest-close proposals as NDJSON; no execution authority")
    parser.add_argument("--bundle", action="store_true", help="emit a proposal with its exact validated CSV snapshots")
    parser.add_argument("--capture-dir", type=Path, help="archive each emitted bundle in an existing private directory before output; not an execution receipt")
    args = parser.parse_args(argv)
    if args.capture_dir is not None and not args.bundle:
        parser.error("--capture-dir requires --bundle")
    try:
        raw = read_signal_input(args.artifact)
        artifact = json.loads(raw, object_pairs_hook=_object, parse_float=_invalid_number, parse_constant=_invalid_number)
        previous = None
        # shortcut: polling captures only observed snapshots; use source-owned delivery for every close.
        while True:
            if args.watch:
                _raise_if_stdout_pipe_closed()
            if previous is not None and read_signal_input(args.artifact) != raw:
                raise ValueError("watch research artifact changed")
            research_bytes = read_signal_input(args.research_bars)
            raw_bars = read_signal_input(args.bars)
            proposal = generate_proposal_from_bytes(raw_bars, research_bytes, artifact)
            if proposal != previous:
                if previous is not None and proposal["data_as_of"] <= previous["data_as_of"]:
                    raise ValueError("watch requires a new close, not rewritten history")
                output = generate_paper_input_bundle(proposal, raw_bars, research_bytes) if args.bundle else proposal
                encoded = _json(output)
                if args.capture_dir is not None:
                    name = proposal['data_as_of'].replace('-', '').replace(':', '') + '-' + proposal['proposal_sha256'] + '.json'
                    try:
                        _capture_bundle(args.capture_dir, name, (encoded + '\n').encode('utf-8'))
                    except (OSError, ValueError):
                        print("Paper input capture failed; no bundle was emitted.", file=sys.stderr)
                        return 1
                print(encoded, flush=True)
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
