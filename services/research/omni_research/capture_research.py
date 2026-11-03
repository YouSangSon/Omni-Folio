"""Daily captured observations evaluated offline without operational authority."""

from __future__ import annotations

import hashlib
import json
import os
import re
import stat
from datetime import date, datetime, timedelta
from decimal import Decimal
from pathlib import Path
from typing import Any

from . import paper_fill
from .engine import Bar, canonical_hash, object_value
from .improve import ExperimentConfig, Parameters, Span, _evaluate, parse_config

MAX_BYTES = 1_048_576
_DECIMAL = re.compile(r"-?(?:0|[1-9][0-9]*)(?:\.[0-9]*[1-9])?")
_CLOCK = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.([0-9]{1,9}))?Z")
_METRICS = (
    "after_cost_return", "max_drawdown", "trade_count", "trade_count_definition", "fill_count",
    "turnover", "fees", "taxes", "slippage_cost", "partial_fill_count",
)
LIMITATIONS = {
    "execution_delay": "delay_bars counts observation rows, not proved source availability or elapsed time",
    "capital_and_liquidation": "each fold resets hypothetical capital and requires terminal liquidation",
    "source_time": "provider timezone, bar boundaries and publication time are not provided; completion is unverified",
    "adjustment": "adjustment request 1 does not prove applied adjustments or comparable price units",
    "authority": "offline research only; no strategy registration, paper proposal, investment advice or execution authority",
}


def _read_input(path: Path) -> bytes:
    with open(path, "rb", opener=lambda name, flags: os.open(name, flags | os.O_NONBLOCK)) as source:
        if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):
            raise ValueError("capture research input must be a regular file")
        raw = source.read(MAX_BYTES + 1)
        if len(raw) > MAX_BYTES:
            raise ValueError("capture research input exceeds 1 MiB")
        return raw


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = item
    return value


def _invalid_number(_: str) -> None:
    raise ValueError("only integer JSON numbers are supported")


def _decode(raw: bytes) -> dict[str, Any]:
    value = json.loads(raw.decode("utf-8"), object_pairs_hook=_unique_object, parse_float=_invalid_number, parse_constant=_invalid_number)
    return object_value(value, "input")


def _closed(value: Any, fields: set[str], name: str) -> dict[str, Any]:
    result = object_value(value, name)
    if set(result) != fields:
        raise ValueError(f"{name} fields are invalid")
    return result


def _canonical_decimal(value: Any, field: str) -> None:
    if not isinstance(value, str) or value == "-0" or _DECIMAL.fullmatch(value) is None:
        raise ValueError(f"{field} must be a canonical decimal string")


def _provider_date(value: Any) -> date:
    if not isinstance(value, str) or re.fullmatch(r"[0-9]{8}", value) is None:
        raise ValueError("provider dates must be 8 ASCII digits")
    return date(int(value[:4]), int(value[4:6]), int(value[6:]))


def _fetch_clock(value: Any) -> tuple[datetime, int]:
    match = _CLOCK.fullmatch(value) if isinstance(value, str) else None
    if match is None:
        raise ValueError("fetch clocks must be UTC timestamps with at most 9 fractional digits")
    return datetime.fromisoformat(value[:19]), int((match[1] or "").ljust(9, "0"))


def _capture_bars(capture: dict[str, Any]) -> list[Bar]:
    _closed(capture, {
        "schema_version", "scope", "provider", "environment", "venue", "currency", "symbol", "interval",
        "request", "fetch_started_at", "fetched_at", "time_evidence", "bounds", "bars",
    }, "capture")
    constants = {
        "schema_version": "kiwoom-candle-capture.v1", "scope": "research_observation", "provider": "kiwoom",
        "environment": "mock", "venue": "XKRX", "currency": "KRW", "interval": "1d",
    }
    if any(capture[key] != expected for key, expected in constants.items()):
        raise ValueError("only native mock daily research captures are supported")
    symbol = capture["symbol"]
    if not isinstance(symbol, str) or re.fullmatch(r"[0-9]{6}", symbol) is None:
        raise ValueError("capture symbol must be 6 ASCII digits")
    request = _closed(capture["request"], {"api_id", "body"}, "request")
    body = _closed(request["body"], {"stk_cd", "base_dt", "upd_stkpc_tp"}, "request.body")
    if request["api_id"] != "ka10081" or body["stk_cd"] != symbol or body["upd_stkpc_tp"] != "1":
        raise ValueError("daily request does not match capture")
    base_date = _provider_date(body["base_dt"])
    started = _fetch_clock(capture["fetch_started_at"])
    fetched = _fetch_clock(capture["fetched_at"])
    if fetched < started:
        raise ValueError("fetch clock order is reversed")
    if base_date != (started[0] + timedelta(hours=9)).date():
        raise ValueError("request base date must match fetch start at fixed UTC+09")
    if capture["time_evidence"] != {
        "provider_field": "dt", "provider_format": "YYYYMMDD", "provider_timezone": "not_provided",
        "bar_boundaries": "not_provided", "publication_time": "not_provided", "bar_completion": "unverified",
    }:
        raise ValueError("daily time evidence is invalid")
    bounds = _closed(capture["bounds"], {"bars_limit", "pages_limit", "selection"}, "bounds")
    if (type(bounds["bars_limit"]) is not int or bounds["bars_limit"] != 500
        or type(bounds["pages_limit"]) is not int or bounds["pages_limit"] != 32
        or bounds["selection"] != "latest_up_to_limit_after_overlap_validation"):
        raise ValueError("capture bounds are invalid")
    rows = capture["bars"]
    if not isinstance(rows, list) or not 1 <= len(rows) <= 500:
        raise ValueError("capture requires 1 to 500 bars")
    bars = []
    previous = None
    for row in rows:
        _closed(row, {"provider_time", "open", "high", "low", "close", "volume"}, "bar")
        label = row["provider_time"]
        day = _provider_date(label)
        if day > base_date or (previous is not None and day <= previous):
            raise ValueError("provider dates must be unique, increasing and no later than request base date")
        previous = day
        values = {}
        for key in ("open", "high", "low", "close", "volume"):
            value = row[key]
            if not isinstance(value, str) or len(value) > 64:
                raise ValueError("source decimal tokens must be at most 64 characters")
            _canonical_decimal(value, f"bar.{key}")
            values[key] = Decimal(value)
        bar = Bar(label, symbol, **values)
        if (min(bar.open, bar.high, bar.low, bar.close) <= 0 or bar.volume < 0
            or bar.low > min(bar.open, bar.close) or bar.high < max(bar.open, bar.close) or bar.low > bar.high):
            raise ValueError("bars require valid positive OHLC and nonnegative volume")
        bars.append(bar)
    return bars


def _capture_config(raw: dict[str, Any]) -> ExperimentConfig:
    if not isinstance(raw, dict) or raw.get("schema_version") != "strategy-improvement.v2":
        raise ValueError("capture research requires strategy-improvement.v2 config")
    for section, fields in (
        ("strategy", ("quantity",)),
        ("execution", ("starting_cash", "fee", "tax", "slippage_bps", "delay_bars", "max_participation")),
        ("promotion", ("minimum_validation_after_cost_return", "minimum_holdout_after_cost_return", "maximum_holdout_max_drawdown")),
    ):
        value = object_value(raw.get(section), section)
        for field in fields:
            _canonical_decimal(value.get(field), f"{section}.{field}")
    return parse_config(raw)


def _parameters(parameters: Parameters) -> dict[str, int]:
    return {"fast_window": parameters.fast_window, "slow_window": parameters.slow_window}


def _metrics(metrics: dict[str, str]) -> dict[str, str]:
    return {key: metrics[key] for key in _METRICS}


def _row(capture: dict[str, Any], index: int) -> dict[str, str | int]:
    return {"row": index, "provider_time": capture["bars"][index]["provider_time"]}


def _range(capture: dict[str, Any], span: Span) -> dict[str, Any]:
    return {"first": _row(capture, span.start), "last": _row(capture, span.stop - 1), "bar_count": span.stop - span.start}


def run_capture_experiment(capture_path: Path, raw_v2_config: dict[str, Any]) -> dict[str, Any]:
    config = _capture_config(raw_v2_config)
    raw = _read_input(capture_path)
    capture = _decode(raw)
    bars = _capture_bars(capture)
    evaluated = _evaluate(bars, config)
    body = {
        "schema_version": "captured-candle-research.v1",
        "scope": "offline_capture_research_only",
        "source": {"sha256": hashlib.sha256(raw).hexdigest(), "capture": capture},
        "config": raw_v2_config,
        "config_sha256": canonical_hash(raw_v2_config),
        "analysis": {
            "arithmetic": {"name": "omni-folio-paper-reference", "version": "1.0.0"},
            "evaluation_policy": {
                "version": paper_fill.EVALUATION_POLICY_VERSION,
                "method": "expanding_walk_forward_then_final_holdout",
                "minimum_fold_bars": config.minimum_fold_bars,
                "selection_metric": "highest training after_cost_return, then lowest training max_drawdown, then windows ascending",
                "final_holdout_evaluated_after_selection": True,
            },
            "pre_holdout": _range(capture, Span(0, evaluated.holdout.start)),
            "candidates": [
                {"parameters": _parameters(item.parameters), "pre_holdout_metrics": _metrics(item.metrics)}
                for item in evaluated.candidates
            ],
            "folds": [
                {
                    "name": fold.name, "train": _range(capture, fold.train), "validation": _range(capture, fold.validation),
                    "selected_parameters": _parameters(fold.selected.parameters), "train_metrics": _metrics(fold.selected.metrics),
                    "validation_metrics": _metrics(fold.validation_metrics),
                }
                for fold in evaluated.folds
            ],
            "selected_parameters": _parameters(evaluated.selected.parameters),
            "selected_pre_holdout_metrics": _metrics(evaluated.selected.metrics),
            "validation_metrics": _metrics(evaluated.validation_metrics),
            "final_holdout": {
                "range": _range(capture, evaluated.holdout), "metrics": _metrics(evaluated.holdout_metrics),
                "trades": [
                    {"signal": _row(capture, trade.signal), "eligible": _row(capture, trade.eligible), "fill": _row(capture, trade.fill),
                     "side": trade.side, "quantity": trade.quantity, "price": trade.price}
                    for trade in evaluated.holdout_trades
                ],
            },
            "buy_and_hold_metrics": _metrics(evaluated.baseline_metrics),
            "threshold_comparisons": {
                "walk_forward_thresholds_met": evaluated.walk_forward_ok,
                "final_holdout_thresholds_met": evaluated.holdout_ok,
                "challenger_exceeds_buy_and_hold": evaluated.baseline_ok,
            },
        },
        "limitations": dict(LIMITATIONS),
    }
    report = {**body, "report_sha256": canonical_hash(body)}
    if len(json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")) + 1 > MAX_BYTES:
        raise ValueError("capture research report exceeds 1 MiB")
    return report
