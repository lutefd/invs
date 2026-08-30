"""Independent, deterministic risk validation for paper targets."""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Mapping, Sequence
from datetime import date
from decimal import Decimal
from typing import Any

from .backtest_inputs import BacktestInputs
from .portfolio import (
    PortfolioState,
    decimal,
    price_row,
    session_decision_at,
    timestamp,
)

_ROUNDING_TOLERANCE = Decimal("1e-24")


def _d(value: Any, field: str) -> Decimal:
    return decimal(value, field=field, non_negative=True)


def _append_reason(codes: list[str], reasons: list[str], code: str, reason: str) -> None:
    if code not in codes:
        codes.append(code)
        reasons.append(reason)


def _session_age(sessions: Sequence[Mapping[str, Any]], observed_at: Any, current: date) -> int:
    observed_date = observed_at.date()
    return sum(
        session["session_date"] > observed_date and session["session_date"] <= current
        for session in sessions
    )


def assess_target(
    account: Mapping[str, Any],
    inputs: BacktestInputs,
    sessions: Sequence[Mapping[str, Any]],
    session_index: int,
    state: PortfolioState,
    target: Mapping[str, Any],
    orders: Sequence[Mapping[str, Any]],
) -> dict[str, Any]:
    """Validate target exposure, liquidity, freshness, and drawdown limits.

    Strategy code cannot bypass this function: callers must persist its
    decision before an order can be approved or filled.
    """

    session = sessions[session_index]
    decision_at = session_decision_at(session)
    policy = account["risk_policy"]
    codes: list[str] = []
    reasons: list[str] = []
    nav = _d(target["nav_base"], "target.nav_base")
    target_values = [
        _d(row["market_value_base"], "target.market_value_base") for row in target["targets"]
    ]
    gross = sum(target_values, Decimal(0)) / nav if nav else Decimal(0)
    max_gross = _d(policy["max_gross_exposure"], "risk.max_gross_exposure")
    if gross > max_gross:
        _append_reason(
            codes,
            reasons,
            "max_gross_exposure",
            f"target gross exposure {gross} exceeds {max_gross}",
        )

    groups: dict[str, dict[str, Decimal]] = {
        "sector": defaultdict(Decimal),
        "country": defaultdict(Decimal),
        "currency": defaultdict(Decimal),
        "theme": defaultdict(Decimal),
    }
    max_position = _d(policy["max_position_weight"], "risk.max_position_weight")
    prohibited = set(policy["prohibited_security_ids"])
    for row in target["targets"]:
        weight = _d(row["target_weight"], "target.target_weight")
        security_id = row["security_id"]
        if weight > max_position:
            _append_reason(
                codes,
                reasons,
                "max_position_weight",
                f"target position {security_id} weight {weight} exceeds {max_position}",
            )
        if weight > 0 and security_id in prohibited:
            _append_reason(
                codes,
                reasons,
                "prohibited_security",
                f"target contains prohibited security {security_id}",
            )
        groups["sector"][row["sector"]] += weight
        groups["country"][row["country"]] += weight
        groups["currency"][row["currency"]] += weight
        for theme in row["themes"]:
            groups["theme"][theme] += weight

    group_limits = {
        "sector": _d(policy["max_sector_exposure"], "risk.max_sector_exposure"),
        "country": _d(policy["max_country_exposure"], "risk.max_country_exposure"),
        "currency": _d(policy["max_currency_exposure"], "risk.max_currency_exposure"),
        "theme": _d(policy["max_theme_exposure"], "risk.max_theme_exposure"),
    }
    for group_name, values in groups.items():
        limit = group_limits[group_name]
        for group, exposure in sorted(values.items()):
            if exposure > limit:
                _append_reason(
                    codes,
                    reasons,
                    f"max_{group_name}_exposure",
                    f"{group_name} exposure {group} is {exposure}, above {limit}",
                )

    total_notional = sum(
        (_d(order["expected_notional_base"], "order.expected_notional_base") for order in orders),
        Decimal(0),
    )
    turnover = total_notional / nav if nav else Decimal(0)
    max_turnover = _d(policy["max_turnover"], "risk.max_turnover")
    if turnover > max_turnover:
        _append_reason(
            codes,
            reasons,
            "max_turnover",
            f"order turnover {turnover} exceeds {max_turnover}",
        )
    max_daily_notional = _d(policy["max_daily_notional"], "risk.max_daily_notional")
    if total_notional > max_daily_notional:
        _append_reason(
            codes,
            reasons,
            "max_daily_notional",
            f"daily notional {total_notional} exceeds {max_daily_notional}",
        )

    minimum_cash = _d(policy["minimum_cash_reserve"], "risk.minimum_cash_reserve")
    estimated_costs = sum(
        (_d(order["expected_cost_base"], "order.expected_cost_base") for order in orders),
        Decimal(0),
    )
    target_cash_after_cost = nav - sum(target_values, Decimal(0)) - estimated_costs
    reserve_tolerance = max(_ROUNDING_TOLERANCE, nav * _ROUNDING_TOLERANCE)
    if target_cash_after_cost + reserve_tolerance < nav * minimum_cash:
        _append_reason(
            codes,
            reasons,
            "minimum_cash_reserve",
            f"projected cash reserve {target_cash_after_cost / nav if nav else Decimal(0)} is below {minimum_cash}",
        )

    max_participation = _d(policy["max_participation"], "risk.max_participation")
    for order in orders:
        row = price_row(
            inputs,
            order["security_id"],
            session["session_date"],
            decision_at,
        )
        if row is None:
            _append_reason(
                codes,
                reasons,
                "missing_price",
                f"no close is available for order {order['security_id']}",
            )
            continue
        quantity = _d(order["quantity"], "order.quantity")
        if row["has_volume"]:
            if quantity > row["volume"] * max_participation:
                _append_reason(
                    codes,
                    reasons,
                    "max_participation",
                    f"order participation for {order['security_id']} exceeds {max_participation}",
                )
        elif max_participation < Decimal(1):
            _append_reason(
                codes,
                reasons,
                "missing_volume",
                f"volume evidence is missing for {order['security_id']}",
            )

    price_gap_limit = min(
        _d(policy["max_price_gap"], "risk.max_price_gap"),
        _d(account["decision_policy"]["max_price_gap"], "decision_policy.max_price_gap"),
    )
    if session_index > 0:
        previous_session = sessions[session_index - 1]
        for row in target["targets"]:
            current_price = price_row(
                inputs,
                row["security_id"],
                session["session_date"],
                decision_at,
            )
            previous_price = price_row(
                inputs,
                row["security_id"],
                previous_session["session_date"],
                decision_at,
            )
            if current_price is None or previous_price is None:
                continue
            gap = abs(current_price["close"] / previous_price["close"] - Decimal(1))
            if gap > price_gap_limit:
                _append_reason(
                    codes,
                    reasons,
                    "max_price_gap",
                    f"price gap for {row['security_id']} is {gap}, above {price_gap_limit}",
                )

    stale_limit = min(
        policy["max_stale_sessions"], account["decision_policy"]["stale_after_sessions"]
    )
    for security_id in {row["security_id"] for row in target["targets"] if _d(row["target_quantity"], "target.target_quantity") > 0}:
        row = price_row(inputs, security_id, session["session_date"], decision_at)
        if row is None:
            continue
        age = _session_age(sessions, row["observed_at"], session["session_date"])
        if age > stale_limit:
            _append_reason(
                codes,
                reasons,
                "stale_data",
                f"price for {security_id} is {age} sessions old, above {stale_limit}",
            )

    if state.peak_nav_base > 0:
        drawdown = max(Decimal(0), (state.peak_nav_base - nav) / state.peak_nav_base)
        max_drawdown = _d(policy["max_drawdown"], "risk.max_drawdown")
        if drawdown > max_drawdown:
            _append_reason(
                codes,
                reasons,
                "max_drawdown",
                f"drawdown {drawdown} exceeds {max_drawdown}",
            )

    status = "rejected" if codes else "approved"
    return {
        "status": status,
        "policy_version": policy["version"],
        "codes": codes,
        "reasons": reasons,
        "checked_at": timestamp(decision_at),
    }


__all__ = ["assess_target"]
