"""Exact arithmetic for the existing Go paper_bar_open_v1 fill contract."""

from __future__ import annotations

from fractions import Fraction
from math import floor

from .engine import decimal_input

MAX_QUANTITY = 4_611_686_018_427_387_903
EVALUATION_POLICY_VERSION = "sma-paper-fill-walk-forward.v1"


def exact_decimal(value: Fraction) -> str:
    denominator = value.denominator
    scales = []
    for factor in (2, 5):
        scale = 0
        while denominator % factor == 0:
            denominator //= factor
            scale += 1
        scales.append(scale)
    if denominator != 1:
        raise ValueError("paper money must have a finite decimal representation")
    rendered = format(value, f".{max(scales)}f")
    return rendered.rstrip("0").rstrip(".") if "." in rendered else rendered


def ratio_string(value: Fraction) -> str:
    # This version ranks and gates the reported ratios, rounded half-even at 28 places.
    rendered = format(value, ".28f").rstrip("0").rstrip(".")
    return "0" if rendered == "-0" else rendered


def calculate_fill(policy: dict[str, str], inputs: dict[str, str]) -> dict[str, str] | None:
    def number(raw: str, field: str) -> Fraction:
        return Fraction(decimal_input(raw, field))

    def integer(raw: str, field: str) -> int:
        value = number(raw, field)
        if value < 0 or value.denominator != 1:
            raise ValueError(f"paper fill {field} must be a non-negative integer")
        return value.numerator

    fee = number(policy["fee"], "fee")
    tax_rate = number(policy["tax"], "tax")
    slippage_bps = number(policy["slippage_bps"], "slippage_bps")
    participation = number(policy["max_participation"], "max_participation")
    if fee < 0 or not 0 <= tax_rate <= 1 or not 0 <= slippage_bps < 10000 or not 0 < participation <= 1:
        raise ValueError("paper fill policy is invalid")
    side = inputs["side"]
    open_price = number(inputs["open"], "open")
    volume = number(inputs["volume"], "volume")
    if side not in {"BUY", "SELL"} or open_price <= 0 or volume < 0:
        raise ValueError("paper fill market input is invalid")
    remaining = integer(inputs["remaining_quantity"], "remaining quantity")
    consumed = integer(inputs["consumed_capacity"], "consumed capacity")
    position = integer(inputs["position_quantity"], "position quantity") if side == "SELL" else 0
    capacity = floor(volume * participation)
    if consumed > capacity:
        raise ValueError("paper fill consumed capacity exceeds capacity")
    quantity = min(capacity - consumed, remaining)
    price = open_price * (1 + slippage_bps / 10000 if side == "BUY" else 1 - slippage_bps / 10000)
    if quantity == 0:
        return None
    if side == "BUY":
        cash = number(inputs["cash"], "cash")
        if cash < 0:
            raise ValueError("paper fill cash is invalid")
        if cash < fee:
            return None
        quantity = min(quantity, floor((cash - fee) / price))
    else:
        quantity = min(quantity, position)
    if quantity == 0:
        return None
    notional = price * quantity
    tax = notional * tax_rate if side == "SELL" else Fraction(0)
    cash_delta = -(notional + fee) if side == "BUY" else notional - fee - tax
    if side == "SELL" and cash_delta < 0:
        raise ValueError("paper fill sell proceeds are negative")
    return {key: exact_decimal(value) for key, value in {
        "quantity": Fraction(quantity), "reference_price": open_price, "price": price,
        "notional": notional, "fee": fee, "tax": tax,
        "slippage": abs(price - open_price) * quantity, "cash_delta": cash_delta,
    }.items()}
