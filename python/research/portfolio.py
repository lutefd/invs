"""Forward-only paper portfolio construction.

This module turns a validated paper account and one point-in-time input set
into an immutable target and deterministic order proposals.  It deliberately
does not approve orders or mutate a ledger; those responsibilities belong to
``risk`` and ``paper`` respectively.
"""

from __future__ import annotations

import hashlib
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, date, datetime
from decimal import ROUND_DOWN, Decimal, InvalidOperation, localcontext
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5

from .backtest_inputs import BacktestInputError, BacktestInputs, load_input_artifacts
from .experiments import canonical_json
from .strategies import StrategySignalError, baseline_targets

PAPER_SCHEMA_VERSION = "1.0.0"

_ZERO = Decimal(0)
_ONE = Decimal(1)
_BPS = Decimal(10_000)
_PRECISION = 80
_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_NON_NEGATIVE_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_UUID = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)

_TARGET_NAMESPACE = UUID("8b1ca8d4-0dd2-5e7a-8ab0-1c770a859c6e")
_ORDER_NAMESPACE = UUID("af884390-6fe2-5f94-b2da-9ee2c7e8d55e")
_CLIENT_ORDER_NAMESPACE = UUID("d9c45f53-df9d-5bf5-9d54-5eb4e1abca41")


class PortfolioError(ValueError):
    """Base error for target construction and order proposal."""


class PaperMissingDataError(PortfolioError):
    """Raised when an input is not usable at the decision or execution clock."""


class PortfolioStateError(PortfolioError):
    """Raised when a reconstructed account state cannot be valued safely."""


@dataclass(frozen=True)
class PortfolioState:
    """The rebuildable account projection consumed by construction and risk."""

    cash_by_currency: Mapping[str, Decimal]
    quantities: Mapping[str, Decimal]
    security_currency: Mapping[str, str]
    nav_base: Decimal
    cash_base: Decimal
    positions_value_base: Decimal
    peak_nav_base: Decimal
    last_session: date | None
    last_decision_session: date | None


def decimal(value: Any, *, field: str, non_negative: bool = False, positive: bool = False) -> Decimal:
    pattern = _NON_NEGATIVE_DECIMAL if non_negative else _DECIMAL
    if not isinstance(value, str) or not pattern.fullmatch(value):
        label = "non-negative" if non_negative else "signed"
        raise PortfolioError(f"{field} must be a canonical {label} decimal string")
    try:
        parsed = Decimal(value)
    except InvalidOperation as error:
        raise PortfolioError(f"{field} must be a decimal string") from error
    if not parsed.is_finite() or (non_negative and parsed < 0) or (positive and parsed <= 0):
        raise PortfolioError(f"{field} has an invalid decimal value")
    return parsed


def decimal_string(value: Decimal | int | str) -> str:
    if not isinstance(value, Decimal):
        value = Decimal(value)
    if not value.is_finite():
        raise PortfolioError("non-finite decimal is not permitted")
    if value == 0:
        return "0"
    text = format(value, "f")
    return text.rstrip("0").rstrip(".") if "." in text else text


def timestamp(value: datetime) -> str:
    parsed = value.astimezone(UTC)
    fraction = f".{parsed.microsecond:06d}".rstrip("0") if parsed.microsecond else ""
    return parsed.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"


def session_decision_at(session: Mapping[str, Any]) -> datetime:
    """Return the recorded information cutoff, defaulting to the session close."""

    value = session.get("decision_at", session["close_at"])
    if not isinstance(value, datetime) or value.tzinfo is None or value.utcoffset() is None:
        raise PortfolioError("session decision_at must be an aware timestamp")
    return value


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise PortfolioError(f"{field} must be a canonical UUID")
    try:
        parsed = UUID(value)
    except ValueError as error:
        raise PortfolioError(f"{field} must be a canonical UUID") from error
    if str(parsed) != value:
        raise PortfolioError(f"{field} must be a canonical UUID")
    return value


def input_fingerprint(references: Sequence[Mapping[str, Any]]) -> str:
    """Hash all immutable input pins, including their admission fitness."""

    pins = []
    for reference in references:
        fitness = reference.get("fitness", reference.get("historical_fitness", "backtest_safe"))
        pins.append(
            {
                "kind": reference["kind"],
                "artifact_id": reference["artifact_id"],
                "path": reference["path"],
                "sha256": reference["sha256"],
                "available_at": reference["available_at"],
                "fitness": fitness,
            }
        )
    pins.sort(key=lambda item: item["kind"])
    return hashlib.sha256(canonical_json(pins)).hexdigest()


def load_paper_inputs(
    references: Sequence[Mapping[str, Any]], *, data_root: str | Path
) -> BacktestInputs:
    """Load paper inputs while admitting only explicitly allowed fitness labels."""

    inputs = load_input_artifacts(
        references,
        data_root=data_root,
        allowed_fitness=("current_research_only", "backtest_safe"),
        required_kinds=("prices", "calendar", "membership"),
        allowed_price_bases=("raw", "split_adjusted"),
    )
    has_split_adjusted_prices = any(row["price_basis"] == "split_adjusted" for row in inputs.prices)
    if has_split_adjusted_prices and inputs.corporate_actions:
        raise BacktestInputError(
            "split_adjusted paper prices cannot be paired with corporate_actions; "
            "use raw prices with actions or omit action inputs"
        )
    return inputs


def sessions_for_account(account: Mapping[str, Any], inputs: BacktestInputs) -> tuple[dict[str, Any], ...]:
    start = date.fromisoformat(account["period"]["start_date"])
    end = date.fromisoformat(account["period"]["end_date"])
    sessions = tuple(row for row in inputs.calendar if start <= row["session_date"] <= end)
    if not sessions:
        raise PaperMissingDataError("calendar has no sessions inside the paper account period")
    for row in sessions:
        if row["available_at"] > row["open_at"]:
            raise PaperMissingDataError(
                f"calendar session {row['session_date']} is not available by its open"
            )
    return sessions


def price_row(
    inputs: BacktestInputs, security_id: str, session_date: date, as_of: datetime
) -> dict[str, Any] | None:
    for row in inputs.prices:
        if row["security_id"] == security_id and row["session_date"] == session_date:
            return row if row["available_at"] <= as_of else None
    return None


def membership_state(
    inputs: BacktestInputs, security_id: str, session_date: date, decision_at: datetime
) -> bool | None:
    candidates = [
        row
        for row in inputs.membership
        if row["security_id"] == security_id
        and row["valid_from"] <= session_date
        and (row["valid_until"] is None or session_date < row["valid_until"])
        and row["available_at"] <= decision_at
    ]
    if not candidates:
        return None
    candidates.sort(key=lambda row: (row["available_at"], row["revision"]))
    rank = (candidates[-1]["available_at"], candidates[-1]["revision"])
    leaders = [row for row in candidates if (row["available_at"], row["revision"]) == rank]
    if len({row["member"] for row in leaders}) != 1:
        raise BacktestInputError(
            f"membership has conflicting equal-ranked assertions for {security_id} on {session_date}"
        )
    return leaders[-1]["member"]


def fx_rate(inputs: BacktestInputs, source: str, target: str, as_of: datetime) -> Decimal:
    if source == target:
        return _ONE
    direct = [
        row
        for row in inputs.fx
        if row["base_currency"] == source
        and row["quote_currency"] == target
        and row["fixing_at"] <= as_of
        and row["available_at"] <= as_of
    ]
    inverse = [
        row
        for row in inputs.fx
        if row["base_currency"] == target
        and row["quote_currency"] == source
        and row["fixing_at"] <= as_of
        and row["available_at"] <= as_of
    ]
    candidates = [(row["fixing_at"], row["rate"]) for row in direct] + [
        (row["fixing_at"], _ONE / row["rate"]) for row in inverse
    ]
    if not candidates:
        raise PaperMissingDataError(f"no point-in-time FX rate supports {source}->{target}")
    candidates.sort(key=lambda item: item[0])
    latest_at = candidates[-1][0]
    latest = [rate for fixing_at, rate in candidates if fixing_at == latest_at]
    if len(set(latest)) != 1:
        raise PortfolioError(f"conflicting FX rates support {source}->{target} at {latest_at}")
    return latest[0]


def convert(
    inputs: BacktestInputs,
    amount: Decimal,
    source: str,
    target: str,
    as_of: datetime,
) -> Decimal:
    with localcontext() as context:
        context.prec = _PRECISION
        return amount * fx_rate(inputs, source, target, as_of)


def cash_base(
    account: Mapping[str, Any], inputs: BacktestInputs, state: PortfolioState, as_of: datetime
) -> Decimal:
    base_currency = account["accounting_policy"]["base_currency"]
    return sum(
        (
            convert(inputs, amount, currency, base_currency, as_of)
            for currency, amount in state.cash_by_currency.items()
        ),
    )


def positions_value(
    account: Mapping[str, Any],
    inputs: BacktestInputs,
    state: PortfolioState,
    session: Mapping[str, Any],
    *,
    require_prices: bool = True,
) -> tuple[Decimal, dict[str, str]]:
    base_currency = account["accounting_policy"]["base_currency"]
    decision_at = session_decision_at(session)
    total = _ZERO
    currencies = dict(state.security_currency)
    for security_id, quantity in sorted(state.quantities.items()):
        if quantity <= 0:
            continue
        row = price_row(inputs, security_id, session["session_date"], decision_at)
        if row is None:
            if require_prices:
                raise PaperMissingDataError(
                    f"missing point-in-time close for held security {security_id} on {session['session_date']}"
                )
            continue
        currencies[security_id] = row["currency"]
        total += convert(
            inputs,
            quantity * row["close"],
            row["currency"],
            base_currency,
            decision_at,
        )
    return total, currencies


def rebalance_due(
    account: Mapping[str, Any], session: Mapping[str, Any], previous: date | None
) -> bool:
    if previous is None:
        return True
    name = account["strategy"]["name"]
    if name == "buy_and_hold":
        return False
    frequency = account["strategy"]["parameters"].get(
        "rebalance_frequency", account["accounting_policy"]["rebalance_frequency"]
    )
    current = session["session_date"]
    if frequency == "daily":
        return True
    if frequency == "weekly":
        return current.isocalendar()[:2] != previous.isocalendar()[:2]
    if frequency == "monthly":
        return current.month != previous.month or current.year != previous.year
    return False


def active_security_ids(
    account: Mapping[str, Any], inputs: BacktestInputs, session: Mapping[str, Any]
) -> tuple[str, ...]:
    decision_at = session_decision_at(session)
    active: list[str] = []
    for security_id in account["universe"]["security_ids"]:
        state = membership_state(
            inputs, security_id, session["session_date"], decision_at
        )
        if state is None:
            raise PaperMissingDataError(
                f"historical membership is unavailable for {security_id} on {session['session_date']}"
            )
        if state:
            active.append(security_id)
    return tuple(sorted(active))


def _signal_targets(
    account: Mapping[str, Any],
    inputs: BacktestInputs,
    sessions: Sequence[Mapping[str, Any]],
    session_index: int,
    active: Sequence[str],
) -> dict[str, Decimal]:
    name = account["strategy"]["name"]
    if name == "equal_weight":
        return baseline_targets("equal_weight", security_ids=active)
    if name == "momentum_12_1":
        decision_at = session_decision_at(sessions[session_index])
        history = {
            security_id: tuple(
                (
                    row["close"]
                    if (row := price_row(
                        inputs,
                        security_id,
                        sessions[history_index]["session_date"],
                        decision_at,
                    ))
                    is not None
                    else None
                )
                for history_index in range(session_index + 1)
            )
            for security_id in active
        }
        try:
            return baseline_targets(
                "momentum_12_1",
                security_ids=active,
                price_history=history,
                session_index=session_index,
                parameters=account["strategy"]["parameters"],
            )
        except StrategySignalError as error:
            raise PaperMissingDataError(str(error)) from error
    if name == "buy_and_hold":
        return baseline_targets("equal_weight", security_ids=active)
    raise PortfolioError(f"unsupported paper strategy {name}")


def _costs(account: Mapping[str, Any], gross: Decimal) -> Decimal:
    policy = account["cost_policy"]
    commission = gross * decimal(policy["commission_bps"], field="commission_bps", non_negative=True) / _BPS
    commission += decimal(policy["fixed_fee"], field="fixed_fee", non_negative=True)
    commission = max(
        commission,
        decimal(policy["minimum_fee"], field="minimum_fee", non_negative=True),
    )
    spread = gross * decimal(policy["spread_bps"], field="spread_bps", non_negative=True) / (_BPS * 2)
    slippage = gross * decimal(policy["slippage_bps"], field="slippage_bps", non_negative=True) / _BPS
    tax = gross * decimal(policy["tax_bps"], field="tax_bps", non_negative=True) / _BPS
    return commission + spread + slippage + tax


def target_orders(
    account: Mapping[str, Any],
    inputs: BacktestInputs,
    sessions: Sequence[Mapping[str, Any]],
    session_index: int,
    target: Mapping[str, Any],
    state: PortfolioState,
) -> tuple[dict[str, Any], ...]:
    """Create deterministic next-session orders from target quantities."""

    if session_index + 1 >= len(sessions):
        return ()
    session = sessions[session_index]
    decision_at = session_decision_at(session)
    execution_session = sessions[session_index + 1]["session_date"].isoformat()
    base_currency = account["accounting_policy"]["base_currency"]
    target_quantities = {
        row["security_id"]: decimal(
            row["target_quantity"], field="target_quantity", non_negative=True
        )
        for row in target["targets"]
    }
    candidates = set(target_quantities) | {
        security_id
        for security_id, quantity in state.quantities.items()
        if quantity > 0
    }
    orders: list[dict[str, Any]] = []
    for security_id in sorted(candidates):
        current = state.quantities.get(security_id, _ZERO)
        target_quantity = target_quantities.get(security_id, _ZERO)
        delta = target_quantity - current
        if delta == 0:
            continue
        row = price_row(inputs, security_id, session["session_date"], decision_at)
        if row is None:
            raise PaperMissingDataError(
                f"no point-in-time close supports an order for {security_id} on {session['session_date']}"
            )
        gross_base = convert(
            inputs,
            abs(delta) * row["close"],
            row["currency"],
            base_currency,
            decision_at,
        )
        order_id = str(
            uuid5(
                _ORDER_NAMESPACE,
                f"{target['target_id']}:{security_id}:{decimal_string(abs(delta))}",
            )
        )
        client_order_id = str(uuid5(_CLIENT_ORDER_NAMESPACE, order_id))
        orders.append(
            {
                "schema_version": PAPER_SCHEMA_VERSION,
                "order_id": order_id,
                "client_order_id": client_order_id,
                "account_id": account["account_id"],
                "target_id": target["target_id"],
                "decision_id": target["decision_id"],
                "target_revision": target["target_revision"],
                "decision_session": session["session_date"].isoformat(),
                "execution_session": execution_session,
                "security_id": security_id,
                "side": "buy" if delta > 0 else "sell",
                "quantity": decimal_string(abs(delta)),
                "reference_price": decimal_string(row["close"]),
                "currency": row["currency"],
                "expected_notional_base": decimal_string(gross_base),
                "expected_cost_base": decimal_string(_costs(account, gross_base)),
                "reason": (
                    "membership_exit"
                    if target_quantities.get(security_id, _ZERO) == 0 and current > 0
                    else account["strategy"]["name"]
                ),
                "status": "proposed",
            }
        )
    return tuple(orders)


def build_target(
    account: Mapping[str, Any],
    inputs: BacktestInputs,
    sessions: Sequence[Mapping[str, Any]],
    session_index: int,
    state: PortfolioState,
    *,
    target_revision: int = 1,
    decision_id: str | None = None,
) -> dict[str, Any]:
    """Build a target using only rows available by the current close."""

    if state.nav_base <= 0:
        raise PortfolioStateError("paper account NAV must be positive before target construction")
    session = sessions[session_index]
    decision_at = session_decision_at(session)
    active = active_security_ids(account, inputs, session)
    for security_id in active:
        if price_row(inputs, security_id, session["session_date"], decision_at) is None:
            raise PaperMissingDataError(
                f"missing current close for active security {security_id} on {session['session_date']}"
            )

    due = rebalance_due(account, session, state.last_decision_session)
    strategy_name = account["strategy"]["name"]
    active_set = set(active)
    selected_weights: dict[str, Decimal]
    selected_quantities: dict[str, Decimal] = {}
    if state.last_decision_session is not None and (strategy_name == "buy_and_hold" or not due):
        selected_weights = {}
        for security_id, quantity in state.quantities.items():
            if quantity > 0 and security_id in active_set:
                row = price_row(inputs, security_id, session["session_date"], decision_at)
                assert row is not None
                value = convert(
                    inputs,
                    quantity * row["close"],
                    row["currency"],
                    account["accounting_policy"]["base_currency"],
                    decision_at,
                )
                selected_weights[security_id] = value / state.nav_base
                selected_quantities[security_id] = quantity
    else:
        selected_weights = _signal_targets(account, inputs, sessions, session_index, active)

    risk_policy = account["risk_policy"]
    minimum_cash = decimal(
        risk_policy["minimum_cash_reserve"], field="minimum_cash_reserve", non_negative=True
    )
    max_gross = decimal(risk_policy["max_gross_exposure"], field="max_gross_exposure", non_negative=True)
    investable_weight = min(_ONE - minimum_cash, max_gross)
    if investable_weight < 0:
        raise PortfolioError("minimum_cash_reserve leaves no investable portfolio")
    initial_target = state.last_decision_session is None
    if (due and (strategy_name != "buy_and_hold" or initial_target)) or not selected_quantities:
        selected_weights = {
            security_id: weight * investable_weight
            for security_id, weight in selected_weights.items()
            if weight > 0
        }

    rows: list[dict[str, Any]] = []
    target_quantities = dict(selected_quantities)
    for security_id, target_weight in sorted(selected_weights.items()):
        row = price_row(inputs, security_id, session["session_date"], decision_at)
        if row is None:
            raise PaperMissingDataError(
                f"missing target price for {security_id} on {session['session_date']}"
            )
        target_value_base = state.nav_base * target_weight
        target_value_local = convert(
            inputs,
            target_value_base,
            account["accounting_policy"]["base_currency"],
            row["currency"],
            decision_at,
        )
        quantity = target_value_local / row["close"]
        if not account["accounting_policy"]["fractional_shares"]:
            quantity = quantity.to_integral_value(rounding=ROUND_DOWN)
        target_quantities[security_id] = quantity

    metadata = {
        row["security_id"]: row
        for row in account["security_metadata"]
    }
    base_currency = account["accounting_policy"]["base_currency"]
    for security_id in sorted(set(target_quantities) | set(state.quantities)):
        quantity = target_quantities.get(security_id, _ZERO)
        if quantity < 0:
            raise PortfolioError(f"negative target quantity for {security_id}")
        if quantity == 0 and state.quantities.get(security_id, _ZERO) <= 0:
            continue
        row = price_row(inputs, security_id, session["session_date"], decision_at)
        if row is None:
            if quantity > 0:
                raise PaperMissingDataError(
                    f"missing target price for {security_id} on {session['session_date']}"
                )
            continue
        meta = metadata[security_id]
        market_value_base = convert(
            inputs,
            quantity * row["close"],
            row["currency"],
            base_currency,
            decision_at,
        )
        rows.append(
            {
                "security_id": security_id,
                "target_weight": decimal_string(market_value_base / state.nav_base),
                "target_quantity": decimal_string(quantity),
                "price": decimal_string(row["close"]),
                "currency": row["currency"],
                "market_value_base": decimal_string(market_value_base),
                "country": meta["country"],
                "sector": meta["sector"],
                "themes": sorted(meta["themes"]),
            }
        )

    pins = [
        {
            "kind": reference["kind"],
            "artifact_id": reference["artifact_id"],
            "sha256": reference["sha256"],
        }
        for reference in sorted(account["inputs"], key=lambda item: item["kind"])
    ]
    fingerprint = input_fingerprint(account["inputs"])
    decision_id = decision_id or str(
        uuid5(_TARGET_NAMESPACE, f"{account['account_id']}:{session['session_date']}:{fingerprint}")
    )
    target_id = str(uuid5(_TARGET_NAMESPACE, f"target:{decision_id}:{target_revision}"))
    cash_value = state.cash_base
    return {
        "schema_version": PAPER_SCHEMA_VERSION,
        "target_id": target_id,
        "account_id": account["account_id"],
        "decision_id": decision_id,
        "target_revision": target_revision,
        "decision_session": session["session_date"].isoformat(),
        "decision_at": timestamp(decision_at),
        "strategy": account["strategy"],
        "input_fingerprint": fingerprint,
        "inputs": pins,
        "nav_base": decimal_string(state.nav_base),
        "cash_base": decimal_string(cash_value),
        "positions_value_base": decimal_string(state.positions_value_base),
        "targets": rows,
        "construction": {
            "objective": (
                "buy_and_hold"
                if strategy_name == "buy_and_hold"
                else "rank_weighted"
                if strategy_name == "momentum_12_1"
                else "equal_weight"
            ),
            "fallback": "cash" if not rows else "none",
            "turnover_penalty": "0",
            "cash_reserve": decimal_string(minimum_cash),
        },
        "risk": {
            "status": "approved",
            "policy_version": risk_policy["version"],
            "codes": [],
            "reasons": [],
            "checked_at": timestamp(decision_at),
        },
    }


__all__ = [
    "PAPER_SCHEMA_VERSION",
    "PaperMissingDataError",
    "PortfolioError",
    "PortfolioState",
    "PortfolioStateError",
    "active_security_ids",
    "build_target",
    "cash_base",
    "convert",
    "decimal",
    "decimal_string",
    "fx_rate",
    "input_fingerprint",
    "load_paper_inputs",
    "membership_state",
    "positions_value",
    "price_row",
    "rebalance_due",
    "session_decision_at",
    "sessions_for_account",
    "target_orders",
    "timestamp",
]
