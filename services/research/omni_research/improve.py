"""Offline, deterministic SMA parameter selection for research-only experiments."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path
from typing import Any

from . import ENGINE_VERSION
from .engine import (
    BPS_DENOMINATOR,
    ONE,
    ZERO,
    Bar,
    canonical_hash,
    decimal_input,
    decimal_string,
    fill,
    load_bars,
    object_value,
    request_execution,
)

MINIMUM_FOLD_BARS = 30
EVALUATION_POLICY_VERSION = "sma-expanding-walk-forward.v1"
DISCLAIMER = "Research-only experiment; not investment advice or a production trading authorization."


@dataclass(frozen=True, order=True)
class Parameters:
    fast_window: int
    slow_window: int


@dataclass(frozen=True)
class ExperimentConfig:
    experiment_id: str
    data_version: str
    strategy_version: str
    quantity: Decimal
    candidates: tuple[Parameters, ...]
    execution: dict[str, Any]
    starting_cash: Decimal
    fee: Decimal
    tax_rate: Decimal
    slippage_bps: Decimal
    delay_bars: int
    participation: Decimal
    train_bars: int
    validation_bars: int
    holdout_bars: int
    minimum_fold_bars: int
    minimum_validation_return: Decimal
    minimum_validation_trades: int
    minimum_holdout_return: Decimal
    maximum_holdout_drawdown: Decimal
    minimum_holdout_trades: int


def integer(value: Any, field: str, *, minimum: int = 0) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < minimum:
        raise ValueError(f"{field} must be an integer of at least {minimum}")
    return value


def parameter_values(value: Any, field: str) -> tuple[int, ...]:
    if not isinstance(value, list) or not value:
        raise ValueError(f"{field} must be a non-empty list")
    values = tuple(integer(item, field, minimum=1) for item in value)
    if len(set(values)) != len(values):
        raise ValueError(f"{field} must not contain duplicates")
    return tuple(sorted(values))


def parse_config(config: dict[str, Any]) -> ExperimentConfig:
    required = {"schema_version", "experiment_id", "data", "strategy", "execution", "splits", "promotion"}
    if not isinstance(config, dict) or set(config) != required or config["schema_version"] != "strategy-improvement.v1":
        raise ValueError("config must be a strategy-improvement.v1 object")
    experiment_id = config["experiment_id"]
    if not isinstance(experiment_id, str) or not experiment_id:
        raise ValueError("experiment_id is required")
    data = object_value(config["data"], "data")
    if set(data) != {"version"} or not isinstance(data["version"], str) or not data["version"]:
        raise ValueError("data.version is required")
    strategy = object_value(config["strategy"], "strategy")
    if set(strategy) != {"name", "version", "quantity", "fast_windows", "slow_windows"}:
        raise ValueError("strategy fields are invalid")
    if strategy["name"] != "long_only_sma_crossover" or not isinstance(strategy["version"], str) or not strategy["version"]:
        raise ValueError("only versioned long_only_sma_crossover is supported")
    quantity = decimal_input(strategy["quantity"], "strategy.quantity")
    if quantity <= ZERO:
        raise ValueError("strategy.quantity must be positive")
    fast_windows = parameter_values(strategy["fast_windows"], "strategy.fast_windows")
    slow_windows = parameter_values(strategy["slow_windows"], "strategy.slow_windows")
    candidates = tuple(Parameters(fast, slow) for fast in fast_windows for slow in slow_windows)
    if len(candidates) > 64 or any(item.fast_window >= item.slow_window for item in candidates):
        raise ValueError("strategy grid must contain at most 64 fast windows strictly below every slow window")

    execution, starting_cash, fee, tax_rate, slippage_bps, delay_bars, participation = request_execution({"execution": config["execution"]})
    if tax_rate > ONE or slippage_bps >= BPS_DENOMINATOR:
        raise ValueError("execution values are out of range")
    splits = object_value(config["splits"], "splits")
    if set(splits) != {"train_bars", "validation_bars", "holdout_bars", "minimum_fold_bars"}:
        raise ValueError("splits fields are invalid")
    minimum_fold_bars = integer(splits["minimum_fold_bars"], "splits.minimum_fold_bars", minimum=MINIMUM_FOLD_BARS)
    train_bars = integer(splits["train_bars"], "splits.train_bars", minimum=minimum_fold_bars)
    validation_bars = integer(splits["validation_bars"], "splits.validation_bars", minimum=minimum_fold_bars)
    holdout_bars = integer(splits["holdout_bars"], "splits.holdout_bars", minimum=minimum_fold_bars)
    if validation_bars < minimum_fold_bars * 2 or validation_bars % minimum_fold_bars != 0:
        raise ValueError("splits.validation_bars must contain at least two complete walk-forward folds")

    promotion = object_value(config["promotion"], "promotion")
    promotion_fields = {
        "baseline",
        "minimum_validation_after_cost_return",
        "minimum_validation_trade_count",
        "minimum_holdout_after_cost_return",
        "maximum_holdout_max_drawdown",
        "minimum_holdout_trade_count",
    }
    if set(promotion) != promotion_fields or promotion["baseline"] != "buy_and_hold":
        raise ValueError("promotion requires the declared buy_and_hold baseline and explicit gates")
    minimum_validation_return = decimal_input(promotion["minimum_validation_after_cost_return"], "promotion.minimum_validation_after_cost_return")
    minimum_holdout_return = decimal_input(promotion["minimum_holdout_after_cost_return"], "promotion.minimum_holdout_after_cost_return")
    maximum_holdout_drawdown = decimal_input(promotion["maximum_holdout_max_drawdown"], "promotion.maximum_holdout_max_drawdown")
    if maximum_holdout_drawdown < ZERO or maximum_holdout_drawdown > ONE:
        raise ValueError("promotion.maximum_holdout_max_drawdown must be in [0, 1]")
    return ExperimentConfig(
        experiment_id=experiment_id,
        data_version=data["version"],
        strategy_version=strategy["version"],
        quantity=quantity,
        candidates=candidates,
        execution=execution,
        starting_cash=starting_cash,
        fee=fee,
        tax_rate=tax_rate,
        slippage_bps=slippage_bps,
        delay_bars=delay_bars,
        participation=participation,
        train_bars=train_bars,
        validation_bars=validation_bars,
        holdout_bars=holdout_bars,
        minimum_fold_bars=minimum_fold_bars,
        minimum_validation_return=minimum_validation_return,
        minimum_validation_trades=integer(promotion["minimum_validation_trade_count"], "promotion.minimum_validation_trade_count", minimum=1),
        minimum_holdout_return=minimum_holdout_return,
        maximum_holdout_drawdown=maximum_holdout_drawdown,
        minimum_holdout_trades=integer(promotion["minimum_holdout_trade_count"], "promotion.minimum_holdout_trade_count", minimum=1),
    )


def average(closes: list[Decimal], end: int, window: int) -> Decimal:
    return sum(closes[end - window + 1 : end + 1], ZERO) / Decimal(window)


def sma_crossover(closes: list[Decimal], end: int, parameters: Parameters) -> str:
    if end < parameters.slow_window:
        return "none"
    previous_fast = average(closes, end - 1, parameters.fast_window)
    previous_slow = average(closes, end - 1, parameters.slow_window)
    current_fast = average(closes, end, parameters.fast_window)
    current_slow = average(closes, end, parameters.slow_window)
    if previous_fast <= previous_slow and current_fast > current_slow:
        return "golden_cross"
    if previous_fast >= previous_slow and current_fast < current_slow:
        return "death_cross"
    return "none"


def simulate_fold(
    bars: list[Bar],
    config: ExperimentConfig,
    parameters: Parameters | None,
    *,
    warmup: list[Bar] | None = None,
) -> tuple[dict[str, str], list[dict[str, str]]]:
    warmup = warmup or []
    series = [*warmup, *bars]
    evaluation_start = len(warmup)
    if parameters is not None and len(series) < parameters.slow_window + config.delay_bars + 2:
        raise ValueError("fold has insufficient bars for the SMA window and delayed liquidation")
    from .engine import Order

    cash = config.starting_cash
    position = ZERO
    fees = taxes = slippage_cost = turnover = ZERO
    orders: list[Order] = []
    trade_trace: list[dict[str, str]] = []
    equity_marks = [cash]
    closes = [bar.close for bar in series]
    liquidation_signal_index = len(series) - config.delay_bars - 1
    order_number = 0

    def schedule(side: str, quantity: Decimal, signal_index: int) -> None:
        nonlocal order_number
        eligible_index = signal_index + config.delay_bars
        if eligible_index <= signal_index:
            raise ValueError("non-positive execution delay would permit lookahead")
        order_number += 1
        orders.append(Order(f"order_{order_number:03d}", side, quantity, signal_index, eligible_index, quantity, []))

    for index in range(evaluation_start, len(series)):
        bar = series[index]
        capacity = bar.volume * config.participation
        for order in orders:
            if order.remaining == ZERO or index < order.eligible_index or capacity <= ZERO:
                continue
            price = bar.open * (ONE + config.slippage_bps / BPS_DENOMINATOR if order.side == "BUY" else ONE - config.slippage_bps / BPS_DENOMINATOR)
            quantity = min(order.remaining, capacity)
            if order.side == "BUY":
                quantity = min(quantity, max(ZERO, (cash - config.fee) / price))
            else:
                quantity = min(quantity, position)
            if quantity <= ZERO:
                continue
            notional, tax, slippage = fill(order, bar, quantity, config.fee, config.tax_rate, config.slippage_bps)
            capacity -= quantity
            fees += config.fee
            taxes += tax
            slippage_cost += slippage
            turnover += notional
            if order.side == "BUY":
                cash -= notional + config.fee
                position += quantity
            else:
                cash += notional - config.fee - tax
                position -= quantity
            trade_trace.append({
                "signal_at": series[order.signal_index].at,
                "eligible_at": series[order.eligible_index].at,
                "fill_at": bar.at,
                "side": order.side,
                "quantity": decimal_string(quantity),
                "price": decimal_string(price),
            })
        equity_marks.append(cash + position * bar.close)
        pending = any(order.remaining != ZERO for order in orders)
        if index == liquidation_signal_index:
            if not pending and position > ZERO:
                schedule("SELL", position, index)
            continue
        if index >= liquidation_signal_index or pending:
            continue
        if parameters is None:
            if index == evaluation_start and position == ZERO:
                schedule("BUY", config.quantity, index)
            continue
        crossover = sma_crossover(closes, index, parameters)
        if position == ZERO and crossover == "golden_cross":
            schedule("BUY", config.quantity, index)
        elif position > ZERO and crossover == "death_cross":
            schedule("SELL", position, index)

    if any(order.remaining != ZERO for order in orders) or position != ZERO:
        raise ValueError("fold cannot complete every delayed long-only order")
    peak = equity_marks[0]
    max_drawdown = ZERO
    for equity in equity_marks:
        peak = max(peak, equity)
        max_drawdown = max(max_drawdown, (peak - equity) / peak)
    partial_fills = sum(1 for order in orders for item in order.fills if item["fill_state"] == "partial")
    completed_round_trips = sum(1 for order in orders if order.side == "BUY" and order.remaining == ZERO)
    return {
        "after_cost_return": decimal_string((cash - config.starting_cash) / config.starting_cash),
        "max_drawdown": decimal_string(max_drawdown),
        "trade_count": decimal_string(Decimal(completed_round_trips)),
        "trade_count_definition": "completed_long_only_round_trips",
        "fill_count": decimal_string(Decimal(len(trade_trace))),
        "turnover": decimal_string(turnover / config.starting_cash),
        "fees": decimal_string(fees),
        "taxes": decimal_string(taxes),
        "slippage_cost": decimal_string(slippage_cost),
        "partial_fill_count": decimal_string(Decimal(partial_fills)),
        "lookahead_violations": "0",
    }, trade_trace


def fold_description(name: str, bars: list[Bar]) -> dict[str, str | int]:
    return {"name": name, "first_bar_at": bars[0].at, "last_bar_at": bars[-1].at, "bar_count": len(bars)}


def aggregate_validation(folds: list[dict[str, str]]) -> dict[str, str]:
    count = Decimal(len(folds))
    return {
        "after_cost_return": decimal_string(sum((Decimal(fold["after_cost_return"]) for fold in folds), ZERO) / count),
        "max_drawdown": decimal_string(max(Decimal(fold["max_drawdown"]) for fold in folds)),
        "trade_count": decimal_string(sum((Decimal(fold["trade_count"]) for fold in folds), ZERO)),
        "trade_count_definition": "sum_of_completed_long_only_round_trips_across_walk_forward_folds",
        "fill_count": decimal_string(sum((Decimal(fold["fill_count"]) for fold in folds), ZERO)),
        "turnover": decimal_string(sum((Decimal(fold["turnover"]) for fold in folds), ZERO)),
        "fees": decimal_string(sum((Decimal(fold["fees"]) for fold in folds), ZERO)),
        "taxes": decimal_string(sum((Decimal(fold["taxes"]) for fold in folds), ZERO)),
        "slippage_cost": decimal_string(sum((Decimal(fold["slippage_cost"]) for fold in folds), ZERO)),
        "partial_fill_count": decimal_string(sum((Decimal(fold["partial_fill_count"]) for fold in folds), ZERO)),
        "lookahead_violations": decimal_string(sum((Decimal(fold["lookahead_violations"]) for fold in folds), ZERO)),
    }


def run_experiment(bars_path: Path, raw_config: dict[str, Any]) -> dict[str, Any]:
    config = parse_config(raw_config)
    bars = load_bars(bars_path)
    if len(bars) != config.train_bars + config.validation_bars + config.holdout_bars:
        raise ValueError("bars must exactly match the configured chronological train, validation, and holdout folds")
    validation_end = config.train_bars + config.validation_bars
    pre_holdout = bars[:validation_end]
    holdout = bars[validation_end:]

    def evaluate_candidates(training: list[Bar]) -> list[dict[str, Any]]:
        evaluated = []
        for parameters in config.candidates:
            metrics, _ = simulate_fold(training, config, parameters)
            evaluated.append({
                "parameters": {"fast_window": parameters.fast_window, "slow_window": parameters.slow_window},
                "metrics": metrics,
            })
        return evaluated

    def select(evaluated: list[dict[str, Any]]) -> dict[str, Any]:
        return min(
            evaluated,
            key=lambda item: (
                -Decimal(item["metrics"]["after_cost_return"]),
                Decimal(item["metrics"]["max_drawdown"]),
                item["parameters"]["fast_window"],
                item["parameters"]["slow_window"],
            ),
        )

    walk_forward_folds: list[dict[str, Any]] = []
    for index, offset in enumerate(range(0, config.validation_bars, config.minimum_fold_bars), start=1):
        train_end = config.train_bars + offset
        fold_train = bars[:train_end]
        fold_validation = bars[train_end : train_end + config.minimum_fold_bars]
        selected = select(evaluate_candidates(fold_train))
        selected_parameters = Parameters(**selected["parameters"])
        validation_metrics, _ = simulate_fold(
            fold_validation,
            config,
            selected_parameters,
            warmup=fold_train,
        )
        walk_forward_folds.append({
            "name": f"walk_forward_{index}",
            "train": fold_description(f"walk_forward_{index}_train", fold_train),
            "validation": fold_description(f"walk_forward_{index}_validation", fold_validation),
            "selected_parameters": selected["parameters"],
            "train_metrics": selected["metrics"],
            "validation_metrics": validation_metrics,
        })

    candidates = evaluate_candidates(pre_holdout)
    challenger = select(candidates)
    challenger_parameters = Parameters(**challenger["parameters"])
    holdout_metrics, holdout_trades = simulate_fold(
        holdout,
        config,
        challenger_parameters,
        warmup=pre_holdout,
    )
    baseline_metrics, _ = simulate_fold(holdout, config, None, warmup=pre_holdout)
    walk_forward_metrics = aggregate_validation([item["validation_metrics"] for item in walk_forward_folds])
    walk_forward_ok = (
        Decimal(walk_forward_metrics["after_cost_return"]) >= config.minimum_validation_return
        and Decimal(walk_forward_metrics["trade_count"]) >= config.minimum_validation_trades
        and all(
            Decimal(item["validation_metrics"]["after_cost_return"]) >= config.minimum_validation_return
            and Decimal(item["validation_metrics"]["trade_count"]) >= config.minimum_validation_trades
            for item in walk_forward_folds
        )
    )
    holdout_ok = (
        Decimal(holdout_metrics["after_cost_return"]) >= config.minimum_holdout_return
        and Decimal(holdout_metrics["max_drawdown"]) <= config.maximum_holdout_drawdown
        and Decimal(holdout_metrics["trade_count"]) >= config.minimum_holdout_trades
    )
    baseline_ok = Decimal(holdout_metrics["after_cost_return"]) > Decimal(baseline_metrics["after_cost_return"])
    target = "paper_candidate" if walk_forward_ok and holdout_ok and baseline_ok else "no_promotion"
    input_sha256 = hashlib.sha256(bars_path.read_bytes()).hexdigest()
    selected_parameters = {
        "quantity": decimal_string(config.quantity),
        **challenger["parameters"],
    }
    body = {
        "schema_version": "strategy-improvement-result.v1",
        "experiment_id": config.experiment_id,
        "input_sha256": input_sha256,
        "config_sha256": canonical_hash(raw_config),
        "manifest": {
            "strategy": {
                "name": "long_only_sma_crossover",
                "version": config.strategy_version,
                "parameters": selected_parameters,
                "parameter_hash": canonical_hash(selected_parameters),
            },
            "data": {"version": config.data_version, "input_sha256": input_sha256},
            "engine": {"name": "omni-folio-reference", "version": ENGINE_VERSION},
            "evaluation_policy": {"version": EVALUATION_POLICY_VERSION},
        },
        "execution": config.execution,
        "evaluation": {
            "method": "expanding_walk_forward_then_final_holdout",
            "minimum_fold_bars": config.minimum_fold_bars,
            "folds": walk_forward_folds,
            "final_holdout": fold_description("final_holdout", holdout),
            "selection_metric": "highest training after_cost_return, then lowest training max_drawdown, then windows ascending",
            "final_holdout_evaluated_after_selection": True,
        },
        "candidates": [
            {"parameters": item["parameters"], "pre_holdout_metrics": item["metrics"]}
            for item in candidates
        ],
        "challenger": {
            "parameters": challenger["parameters"],
            "pre_holdout_metrics": challenger["metrics"],
            "walk_forward_metrics": walk_forward_metrics,
            "walk_forward_folds": walk_forward_folds,
            "final_holdout_metrics": holdout_metrics,
            "final_holdout_trades": holdout_trades,
        },
        "promotion": {
            "target": target,
            "baseline": {
                "name": "buy_and_hold",
                "final_holdout_metrics": baseline_metrics,
                "challenger_exceeds_baseline": baseline_ok,
            },
            "walk_forward_gate_passed": walk_forward_ok,
            "final_holdout_gate_passed": holdout_ok,
            "baseline_gate_passed": baseline_ok,
            "failed_gates": [
                name
                for name, passed in (
                    ("walk_forward", walk_forward_ok),
                    ("final_holdout", holdout_ok),
                    ("buy_and_hold_baseline", baseline_ok),
                )
                if not passed
            ],
        },
        "disclaimer": DISCLAIMER,
    }
    return {**body, "result_sha256": canonical_hash(body)}
