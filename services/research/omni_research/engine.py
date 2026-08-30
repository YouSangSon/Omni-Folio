"""Deterministic, offline-only implementation of contracts/backtest-run.v1."""

from __future__ import annotations

import csv
import hashlib
import json
from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any

ZERO = Decimal("0")
ONE = Decimal("1")
BPS_DENOMINATOR = Decimal("10000")
DISCLAIMER = "Research fixture only; not investment advice."


@dataclass(frozen=True)
class Bar:
    at: str
    symbol: str
    open: Decimal
    high: Decimal
    low: Decimal
    close: Decimal
    volume: Decimal


@dataclass
class Order:
    order_id: str
    side: str
    requested_quantity: Decimal
    signal_index: int
    eligible_index: int
    remaining: Decimal
    fills: list[dict[str, str]]


def decimal_string(value: Decimal) -> str:
    rendered = format(value, "f")
    if "." in rendered:
        rendered = rendered.rstrip("0").rstrip(".")
    return rendered or "0"


def decimal_input(value: Any, field: str) -> Decimal:
    if not isinstance(value, str):
        raise ValueError(f"{field} must be a decimal string")
    try:
        result = Decimal(value)
    except InvalidOperation as error:
        raise ValueError(f"{field} must be a decimal string") from error
    if not result.is_finite() or (result.is_zero() and result.is_signed()) or decimal_string(result) != value:
        raise ValueError(f"{field} must be a canonical decimal string")
    return result


def object_value(value: Any, field: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError(f"{field} must be an object")
    return value


def canonical_hash(value: Any) -> str:
    rendered = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(rendered.encode("utf-8")).hexdigest()


def timestamp(value: str, field: str) -> datetime:
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"{field} must be an RFC 3339 timestamp") from error
    if parsed.tzinfo is None:
        raise ValueError(f"{field} must include a timezone")
    return parsed


def load_bars(path: Path) -> list[Bar]:
    with path.open("r", encoding="utf-8", newline="") as source:
        rows = csv.DictReader(source)
        required = {"bar_at", "symbol", "open", "high", "low", "close", "volume"}
        if rows.fieldnames is None or not required.issubset(rows.fieldnames):
            raise ValueError("bars CSV requires bar_at,symbol,open,high,low,close,volume columns")
        bars = [
            Bar(
                at=row["bar_at"],
                symbol=row["symbol"],
                open=decimal_input(row["open"], "bar.open"),
                high=decimal_input(row["high"], "bar.high"),
                low=decimal_input(row["low"], "bar.low"),
                close=decimal_input(row["close"], "bar.close"),
                volume=decimal_input(row["volume"], "bar.volume"),
            )
            for row in rows
        ]
    if not bars or len({bar.symbol for bar in bars}) != 1:
        raise ValueError("buy_and_hold.v1 requires at least one bar for exactly one symbol")
    if any(
        not bar.symbol
        or min(bar.open, bar.high, bar.low, bar.close) <= ZERO
        or bar.low > min(bar.open, bar.close)
        or bar.high < max(bar.open, bar.close)
        or bar.low > bar.high
        or bar.volume < ZERO
        for bar in bars
    ):
        raise ValueError("bars require a symbol, valid positive OHLC prices, and non-negative volume")
    parsed_times = [timestamp(bar.at, "bar_at") for bar in bars]
    if parsed_times != sorted(parsed_times) or len(set(parsed_times)) != len(parsed_times):
        raise ValueError("bars must be strictly ordered by unique bar_at")
    return bars


def request_execution(request: dict[str, Any]) -> tuple[dict[str, Any], Decimal, Decimal, Decimal, Decimal, int, Decimal]:
    execution = object_value(request.get("execution"), "execution")
    required = {"starting_cash", "fee", "tax", "slippage_bps", "delay_bars", "max_participation", "signal_price", "fill_price"}
    if set(execution) != required:
        raise ValueError("execution fields do not match backtest-run.v1")
    if execution["signal_price"] != "bar_close" or execution["fill_price"] != "next_eligible_bar_open":
        raise ValueError("only close signals and next eligible open fills are supported")
    starting_cash = decimal_input(execution["starting_cash"], "execution.starting_cash")
    fee = decimal_input(execution["fee"], "execution.fee")
    tax = decimal_input(execution["tax"], "execution.tax")
    slippage_bps = decimal_input(execution["slippage_bps"], "execution.slippage_bps")
    delay = decimal_input(execution["delay_bars"], "execution.delay_bars")
    participation = decimal_input(execution["max_participation"], "execution.max_participation")
    if starting_cash <= ZERO or min(fee, tax, slippage_bps) < ZERO or not ZERO < participation <= ONE:
        raise ValueError("execution values are out of range")
    if delay < ONE or delay != delay.to_integral_value():
        raise ValueError("execution.delay_bars must be a whole number of at least 1 to prevent lookahead")
    return execution, starting_cash, fee, tax, slippage_bps, int(delay), participation


def fill(order: Order, bar: Bar, quantity: Decimal, fee: Decimal, tax_rate: Decimal, slippage_bps: Decimal) -> tuple[Decimal, Decimal, Decimal]:
    price = bar.open * (ONE + slippage_bps / BPS_DENOMINATOR if order.side == "BUY" else ONE - slippage_bps / BPS_DENOMINATOR)
    notional = price * quantity
    tax = notional * tax_rate if order.side == "SELL" else ZERO
    order.remaining -= quantity
    fill_number = len(order.fills) + 1
    order.fills.append({
        "fill_id": f"fill_{order.side.lower()}_{fill_number:03d}",
        "bar_at": bar.at,
        "quantity": decimal_string(quantity),
        "price": decimal_string(price),
        "notional": decimal_string(notional),
        "fee": decimal_string(fee),
        "tax": decimal_string(tax),
        "fill_state": "complete" if order.remaining == ZERO else "partial",
    })
    return notional, tax, abs(price - bar.open) * quantity


def build_manifest(
    bars_path: Path,
    request: dict[str, Any],
    *,
    run_id: str,
    dataset_id: str,
    started_at: str,
    finished_at: str,
) -> dict[str, Any]:
    if not run_id or not dataset_id:
        raise ValueError("run_id and dataset_id are required")
    if timestamp(finished_at, "finished_at") < timestamp(started_at, "started_at"):
        raise ValueError("finished_at must not precede started_at")
    if not isinstance(request, dict) or request.get("schema_version") != "backtest-request.v1":
        raise ValueError("request must be a backtest-request.v1 object")
    strategy = object_value(request.get("strategy"), "strategy")
    data = object_value(request.get("data"), "data")
    engine = object_value(request.get("engine"), "engine")
    required_strategy = {"name", "version", "parameters", "parameter_hash"}
    required_data = {"path", "version", "input_sha256", "bar_interval", "timezone"}
    if set(strategy) != required_strategy or set(data) != required_data or set(engine) != {"name", "version"}:
        raise ValueError("request fields do not match backtest-run.v1")
    if engine != {"name": "omni-folio-reference", "version": "0.1.0"}:
        raise ValueError("request engine does not match this implementation")
    parameters = object_value(strategy["parameters"], "strategy.parameters")
    if strategy["name"] != "buy_and_hold" or set(parameters) != {"quantity", "liquidate_at_end"} or parameters["liquidate_at_end"] is not True:
        raise ValueError("only buy_and_hold with liquidate_at_end=true is supported")
    quantity = decimal_input(parameters["quantity"], "strategy.parameters.quantity")
    if quantity <= ZERO or strategy["parameter_hash"] != canonical_hash(parameters):
        raise ValueError("strategy parameters or parameter_hash are invalid")
    if data["bar_interval"] != "1d" or data["timezone"] != "UTC":
        raise ValueError("only UTC daily bars are supported")
    raw_bars = bars_path.read_bytes()
    if data["input_sha256"] != hashlib.sha256(raw_bars).hexdigest():
        raise ValueError("data.input_sha256 does not match bars")
    execution, starting_cash, fee, tax_rate, slippage_bps, delay, participation = request_execution(request)
    bars = load_bars(bars_path)
    if len(bars) <= delay:
        raise ValueError("not enough bars for delayed execution")

    buy = Order("order_buy_001", "BUY", quantity, 0, delay, quantity, [])
    sell_signal_index = len(bars) - delay - 1
    sell = Order("order_sell_001", "SELL", quantity, sell_signal_index, sell_signal_index + delay, quantity, [])
    orders = [buy, sell]
    cash = starting_cash
    position = ZERO
    fees = taxes = slippage_cost = turnover = ZERO
    equity_marks = [cash]
    for index, bar in enumerate(bars):
        capacity = bar.volume * participation
        for order in orders:
            if order.remaining == ZERO or index < order.eligible_index or capacity <= ZERO:
                continue
            fill_quantity = min(order.remaining, capacity)
            price = bar.open * (ONE + slippage_bps / BPS_DENOMINATOR if order.side == "BUY" else ONE - slippage_bps / BPS_DENOMINATOR)
            if order.side == "BUY":
                fill_quantity = min(fill_quantity, max(ZERO, (cash - fee) / price))
            else:
                fill_quantity = min(fill_quantity, position)
            if fill_quantity <= ZERO:
                continue
            notional, fill_tax, fill_slippage = fill(order, bar, fill_quantity, fee, tax_rate, slippage_bps)
            capacity -= fill_quantity
            fees += fee
            taxes += fill_tax
            slippage_cost += fill_slippage
            turnover += notional
            if order.side == "BUY":
                cash -= notional + fee
                position += fill_quantity
            else:
                cash += notional - fee - fill_tax
                position -= fill_quantity
        equity_marks.append(cash + position * bar.close)
    if any(order.remaining != ZERO for order in orders) or position != ZERO:
        raise ValueError("insufficient eligible bars, cash, or position for a complete liquidating run")
    peak = equity_marks[0]
    max_drawdown = ZERO
    for equity in equity_marks:
        peak = max(peak, equity)
        if peak:
            max_drawdown = max(max_drawdown, (peak - equity) / peak)
    partial_fills = sum(1 for order in orders for item in order.fills if item["fill_state"] == "partial")
    realized_pnl = cash - starting_cash
    result_orders = [
        {
            "order_id": order.order_id,
            "symbol": bars[0].symbol,
            "side": order.side,
            "requested_quantity": decimal_string(order.requested_quantity),
            "signal_at": bars[order.signal_index].at,
            "first_eligible_at": bars[order.eligible_index].at,
            "first_fill_at": order.fills[0]["bar_at"],
            "fills": order.fills,
        }
        for order in orders
    ]
    return {
        "schema_version": "backtest-run.v1",
        "run_id": run_id,
        "status": "complete",
        "manifest": {
            "strategy": strategy,
            "data": {
                "dataset_id": dataset_id,
                "version": data["version"],
                "input_sha256": data["input_sha256"],
                "bar_interval": data["bar_interval"],
                "timezone": data["timezone"],
            },
            "engine": engine,
            "execution": execution,
            "started_at": started_at,
            "finished_at": finished_at,
        },
        "result": {
            "currency": "USD",
            "metrics": {
                "starting_cash": decimal_string(starting_cash),
                "ending_cash": decimal_string(cash),
                "ending_equity": decimal_string(cash),
                "total_return": decimal_string(realized_pnl / starting_cash),
                "realized_pnl": decimal_string(realized_pnl),
                "unrealized_pnl": "0",
                "fees": decimal_string(fees),
                "taxes": decimal_string(taxes),
                "slippage_cost": decimal_string(slippage_cost),
                "turnover": decimal_string(turnover / starting_cash),
                "max_drawdown": decimal_string(max_drawdown),
                "partial_fill_count": decimal_string(Decimal(partial_fills)),
                "lookahead_violations": "0",
            },
            "orders": result_orders,
            "positions": [],
        },
        "disclaimer": DISCLAIMER,
    }
