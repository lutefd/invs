"""Deterministic, daily point-in-time backtest simulation.

This module is intentionally small and inspectable.  It consumes only the
validated local artifacts loaded by :mod:`research.backtest_inputs`; strategy
signals produce target weights and the engine owns orders, fills, actions,
cash, valuation, and the audit ledger.
"""

from __future__ import annotations

import json
import os
import re
import tempfile
from collections import defaultdict
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from decimal import ROUND_DOWN, Decimal, InvalidOperation, localcontext
from itertools import pairwise
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .backtest_inputs import BacktestInputError, BacktestInputs, load_backtest_inputs
from .experiments import (
    ENGINE_VERSION,
    BacktestSpecError,
    canonical_json,
    experiment_sha256,
    input_fingerprint,
    sha256_bytes,
    validate_experiment_spec,
)
from .strategies import baseline_targets

BACKTEST_SCHEMA_VERSION: Final[str] = "1.0.0"
CHECKPOINT_SCHEMA_VERSION: Final[str] = "1.0.0"
METRICS_VERSION: Final[str] = "1.0.0"
ANNUALIZATION_FACTOR: Final[int] = 252

_ORDER_NAMESPACE = UUID("f8e338fb-fb10-59c6-a9b9-87ad4ddbf99d")
_FILL_NAMESPACE = UUID("c5128a3c-e7ae-5227-ae1d-9feaa14f6d4e")
_RESULT_NAMESPACE = UUID("0b82ccec-4b5e-5e8e-8dd6-6ae7f327d0a2")
_ZERO = Decimal(0)
_ONE = Decimal(1)
_BPS = Decimal(10_000)
_PRECISION = 80


class BacktestError(BacktestSpecError):
    """Base error for a failed or unsafe simulation."""


class BacktestMissingDataError(BacktestError):
    """Raised when the declared point-in-time data cannot support a required event."""


class BacktestAccountingError(BacktestError):
    """Raised when a portfolio accounting invariant would be violated."""


class BacktestCheckpointError(BacktestError):
    """Raised when an append-only recovery checkpoint is invalid or conflicts."""


class BacktestInterruptedError(BacktestError):
    """Raised by the operator path after a durable checkpointed interruption."""


@dataclass(frozen=True)
class BacktestRun:
    """Deterministic simulation outputs before filesystem publication."""

    result_id: str
    experiment_id: str
    experiment_sha256: str
    input_fingerprint: str
    engine_version: str
    context: dict[str, Any]
    nav: tuple[dict[str, Any], ...]
    holdings: tuple[dict[str, Any], ...]
    orders: tuple[dict[str, Any], ...]
    fills: tuple[dict[str, Any], ...]
    ledger: tuple[dict[str, Any], ...]
    metrics: dict[str, Any]
    summary: dict[str, int]


@dataclass
class _PendingOrder:
    index: int
    security_id: str
    side: str
    quantity: Decimal
    execution_date: date
    currency: str


@dataclass
class _State:
    cash: dict[str, Decimal]
    quantities: dict[str, Decimal] = field(default_factory=lambda: defaultdict(Decimal))
    security_currency: dict[str, str] = field(default_factory=dict)
    orders: list[dict[str, Any]] = field(default_factory=list)
    fills: list[dict[str, Any]] = field(default_factory=list)
    ledger: list[dict[str, Any]] = field(default_factory=list)
    pending: list[_PendingOrder] = field(default_factory=list)
    processed_actions: set[str] = field(default_factory=set)
    hold_weights: dict[str, Decimal] = field(default_factory=dict)
    hold_initialized: bool = False
    last_decision_date: date | None = None
    last_nav: Decimal = _ZERO
    benchmark_units: Decimal | None = None
    benchmark_value_start: Decimal | None = None
    next_ledger_sequence: int = 1
    total_fee_base: Decimal = _ZERO
    total_spread_base: Decimal = _ZERO
    total_slippage_base: Decimal = _ZERO
    total_tax_base: Decimal = _ZERO
    total_gross_base: Decimal = _ZERO
    cost_by_session: dict[date, Decimal] = field(default_factory=lambda: defaultdict(Decimal))
    rejected_trade_count: int = 0
    out_of_market_sessions: int = 0


_CHECKPOINT_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_CHECKPOINT_FIELDS = frozenset(
    {"schema_version", "checkpoint_sha256", "payload"}
)
_CHECKPOINT_PAYLOAD_FIELDS = frozenset(
    {
        "experiment_id",
        "experiment_sha256",
        "input_fingerprint",
        "engine_version",
        "next_session_index",
        "state",
        "nav",
        "holdings",
    }
)
_CHECKPOINT_STATE_FIELDS = frozenset(
    {
        "cash",
        "quantities",
        "security_currency",
        "orders",
        "fills",
        "ledger",
        "pending",
        "processed_actions",
        "hold_weights",
        "hold_initialized",
        "last_decision_date",
        "last_nav",
        "benchmark_units",
        "benchmark_value_start",
        "next_ledger_sequence",
        "total_fee_base",
        "total_spread_base",
        "total_slippage_base",
        "total_tax_base",
        "total_gross_base",
        "cost_by_session",
        "rejected_trade_count",
        "out_of_market_sessions",
    }
)
_CHECKPOINT_PENDING_FIELDS = frozenset(
    {"index", "security_id", "side", "quantity", "execution_date", "currency"}
)


def _checkpoint_strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise BacktestCheckpointError(f"invalid checkpoint {path}: {error}") from error
    if not isinstance(document, dict):
        raise BacktestCheckpointError(f"checkpoint {path} must be an object")
    return document


def _checkpoint_exact_fields(value: Mapping[str, Any], expected: frozenset[str], label: str) -> None:
    actual = frozenset(value)
    if actual != expected:
        missing = sorted(expected - actual)
        extra = sorted(actual - expected)
        raise BacktestCheckpointError(f"{label} fields mismatch: missing={missing}, extra={extra}")


def _checkpoint_decimal(value: Any, *, field: str) -> Decimal:
    if not isinstance(value, str) or not _CHECKPOINT_DECIMAL.fullmatch(value):
        raise BacktestCheckpointError(f"{field} must be a canonical decimal string")
    try:
        return _d(value)
    except BacktestAccountingError as error:
        raise BacktestCheckpointError(f"{field} is invalid: {error}") from error


def _checkpoint_decimal_map(value: Any, *, field: str) -> dict[str, Decimal]:
    if not isinstance(value, Mapping):
        raise BacktestCheckpointError(f"{field} must be an object")
    result: dict[str, Decimal] = {}
    for key, item in value.items():
        if not isinstance(key, str) or not key:
            raise BacktestCheckpointError(f"{field} has an invalid key")
        result[key] = _checkpoint_decimal(item, field=f"{field}.{key}")
    return result


def _checkpoint_list(value: Any, *, field: str) -> list[Any]:
    if not isinstance(value, list):
        raise BacktestCheckpointError(f"{field} must be an array")
    return list(value)


def _state_checkpoint_document(state: _State) -> dict[str, Any]:
    return {
        "cash": {key: _dstr(value) for key, value in sorted(state.cash.items())},
        "quantities": {key: _dstr(value) for key, value in sorted(state.quantities.items())},
        "security_currency": dict(sorted(state.security_currency.items())),
        "orders": list(state.orders),
        "fills": list(state.fills),
        "ledger": list(state.ledger),
        "pending": [
            {
                "index": order.index,
                "security_id": order.security_id,
                "side": order.side,
                "quantity": _dstr(order.quantity),
                "execution_date": order.execution_date.isoformat(),
                "currency": order.currency,
            }
            for order in sorted(state.pending, key=lambda item: item.index)
        ],
        "processed_actions": sorted(state.processed_actions),
        "hold_weights": {key: _dstr(value) for key, value in sorted(state.hold_weights.items())},
        "hold_initialized": state.hold_initialized,
        "last_decision_date": (
            state.last_decision_date.isoformat() if state.last_decision_date is not None else None
        ),
        "last_nav": _dstr(state.last_nav),
        "benchmark_units": (
            _dstr(state.benchmark_units) if state.benchmark_units is not None else None
        ),
        "benchmark_value_start": (
            _dstr(state.benchmark_value_start) if state.benchmark_value_start is not None else None
        ),
        "next_ledger_sequence": state.next_ledger_sequence,
        "total_fee_base": _dstr(state.total_fee_base),
        "total_spread_base": _dstr(state.total_spread_base),
        "total_slippage_base": _dstr(state.total_slippage_base),
        "total_tax_base": _dstr(state.total_tax_base),
        "total_gross_base": _dstr(state.total_gross_base),
        "cost_by_session": {
            key.isoformat(): _dstr(value) for key, value in sorted(state.cost_by_session.items())
        },
        "rejected_trade_count": state.rejected_trade_count,
        "out_of_market_sessions": state.out_of_market_sessions,
    }


def _checkpoint_payload(
    *,
    normalized: Mapping[str, Any],
    spec_hash: str,
    input_hash: str,
    next_session_index: int,
    state: _State,
    nav_rows: list[dict[str, Any]],
    holding_rows: list[dict[str, Any]],
) -> dict[str, Any]:
    return {
        "experiment_id": normalized["experiment_id"],
        "experiment_sha256": spec_hash,
        "input_fingerprint": input_hash,
        "engine_version": ENGINE_VERSION,
        "next_session_index": next_session_index,
        "state": _state_checkpoint_document(state),
        "nav": list(nav_rows),
        "holdings": list(holding_rows),
    }


def _checkpoint_document(payload: Mapping[str, Any]) -> tuple[dict[str, Any], bytes]:
    payload_bytes = canonical_json(payload) + b"\n"
    checkpoint_hash = sha256_bytes(payload_bytes)
    document = {
        "schema_version": CHECKPOINT_SCHEMA_VERSION,
        "checkpoint_sha256": checkpoint_hash,
        "payload": payload,
    }
    return document, canonical_json(document) + b"\n"


def _write_checkpoint(path: Path, payload: Mapping[str, Any]) -> str:
    document, content = _checkpoint_document(payload)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=f".{path.name}.", delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path)
        except FileExistsError:
            try:
                existing = path.read_bytes()
            except OSError as error:
                raise BacktestCheckpointError(f"cannot read existing checkpoint {path}: {error}") from error
            if existing != content:
                raise BacktestCheckpointError(f"immutable checkpoint path conflicts: {path}")
        finally:
            temporary.unlink(missing_ok=True)
    except OSError as error:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        raise BacktestCheckpointError(f"cannot publish checkpoint {path}: {error}") from error
    return document["checkpoint_sha256"]


def _checkpoint_directory(checkpoint_root: str | Path, experiment_id: str) -> Path:
    return Path(checkpoint_root).expanduser().resolve() / f"experiment-{experiment_id}"


def _latest_checkpoint(directory: Path) -> tuple[Path, int] | None:
    if not directory.exists():
        return None
    if not directory.is_dir():
        raise BacktestCheckpointError(f"checkpoint path is not a directory: {directory}")
    candidates: list[tuple[Path, int]] = []
    for path in directory.glob("checkpoint-*.json"):
        match = re.fullmatch(r"checkpoint-([0-9]{6})\.json", path.name)
        if match is None:
            raise BacktestCheckpointError(f"checkpoint directory contains an invalid file: {path.name}")
        candidates.append((path, int(match.group(1))))
    if not candidates:
        return None
    return max(candidates, key=lambda item: item[1])


def _restore_checkpoint(
    path: Path,
    *,
    expected_experiment_id: str,
    expected_spec_hash: str,
    expected_input_hash: str,
    expected_engine_version: str,
    sessions: tuple[dict[str, Any], ...],
    expected_index: int,
) -> tuple[_State, list[dict[str, Any]], list[dict[str, Any]], int]:
    document = _checkpoint_strict_json(path)
    _checkpoint_exact_fields(document, _CHECKPOINT_FIELDS, "checkpoint")
    if document["schema_version"] != CHECKPOINT_SCHEMA_VERSION:
        raise BacktestCheckpointError(f"checkpoint {path} has an unsupported schema_version")
    checkpoint_hash = document["checkpoint_sha256"]
    if not isinstance(checkpoint_hash, str) or not re.fullmatch(r"^[0-9a-f]{64}$", checkpoint_hash):
        raise BacktestCheckpointError(f"checkpoint {path} has an invalid checkpoint_sha256")
    payload = document["payload"]
    if not isinstance(payload, Mapping):
        raise BacktestCheckpointError(f"checkpoint {path}.payload must be an object")
    _checkpoint_exact_fields(payload, _CHECKPOINT_PAYLOAD_FIELDS, "checkpoint payload")
    expected_document, _ = _checkpoint_document(payload)
    if expected_document["checkpoint_sha256"] != checkpoint_hash:
        raise BacktestCheckpointError(f"checkpoint {path} hash does not match its payload")
    for expected_field, expected in (
        ("experiment_id", expected_experiment_id),
        ("experiment_sha256", expected_spec_hash),
        ("input_fingerprint", expected_input_hash),
        ("engine_version", expected_engine_version),
    ):
        if payload[expected_field] != expected:
            raise BacktestCheckpointError(
                f"checkpoint {path} {expected_field} does not match the current run"
            )
    next_index = payload["next_session_index"]
    if not isinstance(next_index, int) or isinstance(next_index, bool) or next_index != expected_index:
        raise BacktestCheckpointError(f"checkpoint {path} has an invalid next_session_index")
    if next_index < 1 or next_index > len(sessions):
        raise BacktestCheckpointError(f"checkpoint {path} next_session_index is outside the session range")

    state_document = payload["state"]
    if not isinstance(state_document, Mapping):
        raise BacktestCheckpointError(f"checkpoint {path}.payload.state must be an object")
    _checkpoint_exact_fields(state_document, _CHECKPOINT_STATE_FIELDS, "checkpoint state")
    cash = _checkpoint_decimal_map(state_document["cash"], field="checkpoint state.cash")
    quantities = _checkpoint_decimal_map(state_document["quantities"], field="checkpoint state.quantities")
    hold_weights = _checkpoint_decimal_map(state_document["hold_weights"], field="checkpoint state.hold_weights")
    security_currency = state_document["security_currency"]
    if not isinstance(security_currency, Mapping) or any(
        not isinstance(key, str) or not isinstance(value, str) or not value
        for key, value in security_currency.items()
    ):
        raise BacktestCheckpointError("checkpoint state.security_currency must map strings to currencies")
    processed_actions = _checkpoint_list(state_document["processed_actions"], field="checkpoint state.processed_actions")
    if any(not isinstance(item, str) or not item for item in processed_actions):
        raise BacktestCheckpointError("checkpoint state.processed_actions contains an invalid action")
    if len(set(processed_actions)) != len(processed_actions):
        raise BacktestCheckpointError("checkpoint state.processed_actions contains duplicates")
    pending_document = _checkpoint_list(state_document["pending"], field="checkpoint state.pending")
    pending: list[_PendingOrder] = []
    for index, raw in enumerate(pending_document):
        if not isinstance(raw, Mapping):
            raise BacktestCheckpointError(f"checkpoint state.pending[{index}] must be an object")
        _checkpoint_exact_fields(raw, _CHECKPOINT_PENDING_FIELDS, f"checkpoint state.pending[{index}]")
        order_index = raw["index"]
        if not isinstance(order_index, int) or isinstance(order_index, bool) or order_index < 0:
            raise BacktestCheckpointError(f"checkpoint state.pending[{index}].index is invalid")
        if raw["side"] not in {"buy", "sell"}:
            raise BacktestCheckpointError(f"checkpoint state.pending[{index}].side is invalid")
        try:
            execution_date = date.fromisoformat(raw["execution_date"])
        except (TypeError, ValueError) as error:
            raise BacktestCheckpointError(f"checkpoint state.pending[{index}].execution_date is invalid") from error
        pending.append(
            _PendingOrder(
                order_index,
                raw["security_id"],
                raw["side"],
                _checkpoint_decimal(raw["quantity"], field=f"checkpoint state.pending[{index}].quantity"),
                execution_date,
                raw["currency"],
            )
        )
    pending.sort(key=lambda item: item.index)
    orders = _checkpoint_list(state_document["orders"], field="checkpoint state.orders")
    fills = _checkpoint_list(state_document["fills"], field="checkpoint state.fills")
    ledger = _checkpoint_list(state_document["ledger"], field="checkpoint state.ledger")
    for row_field, rows in (("orders", orders), ("fills", fills), ("ledger", ledger)):
        if any(not isinstance(row, Mapping) for row in rows):
            raise BacktestCheckpointError(f"checkpoint state.{row_field} contains a non-object row")
    if any(order.index >= len(orders) for order in pending):
        raise BacktestCheckpointError("checkpoint state.pending references an unknown order")
    last_decision_value = state_document["last_decision_date"]
    if last_decision_value is None:
        last_decision_date = None
    else:
        try:
            last_decision_date = date.fromisoformat(last_decision_value)
        except (TypeError, ValueError) as error:
            raise BacktestCheckpointError("checkpoint state.last_decision_date is invalid") from error
    hold_initialized = state_document["hold_initialized"]
    if not isinstance(hold_initialized, bool):
        raise BacktestCheckpointError("checkpoint state.hold_initialized must be boolean")
    next_ledger_sequence = state_document["next_ledger_sequence"]
    if not isinstance(next_ledger_sequence, int) or isinstance(next_ledger_sequence, bool) or next_ledger_sequence < 1:
        raise BacktestCheckpointError("checkpoint state.next_ledger_sequence is invalid")
    for count_field in ("rejected_trade_count", "out_of_market_sessions"):
        value = state_document[count_field]
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            raise BacktestCheckpointError(f"checkpoint state.{count_field} is invalid")
    cost_by_session_document = state_document["cost_by_session"]
    if not isinstance(cost_by_session_document, Mapping):
        raise BacktestCheckpointError("checkpoint state.cost_by_session must be an object")
    cost_by_session: dict[date, Decimal] = {}
    for key, value in cost_by_session_document.items():
        try:
            parsed_date = date.fromisoformat(key)
        except (TypeError, ValueError) as error:
            raise BacktestCheckpointError("checkpoint state.cost_by_session has an invalid date") from error
        cost_by_session[parsed_date] = _checkpoint_decimal(value, field=f"checkpoint state.cost_by_session.{key}")

    def optional_decimal(field: str) -> Decimal | None:
        value = state_document[field]
        if value is None:
            return None
        return _checkpoint_decimal(value, field=f"checkpoint state.{field}")

    nav_rows = _checkpoint_list(payload["nav"], field="checkpoint payload.nav")
    holding_rows = _checkpoint_list(payload["holdings"], field="checkpoint payload.holdings")
    if len(nav_rows) != next_index:
        raise BacktestCheckpointError(f"checkpoint {path} NAV row count does not match next_session_index")
    for index, row in enumerate(nav_rows):
        if not isinstance(row, Mapping) or row.get("session_date") != sessions[index]["session_date"].isoformat():
            raise BacktestCheckpointError(f"checkpoint {path} NAV rows are not session ordered")
    if not nav_rows:
        raise BacktestCheckpointError(f"checkpoint {path} has no NAV rows")
    if _checkpoint_decimal(nav_rows[-1].get("nav"), field="checkpoint payload.nav[-1].nav") != _checkpoint_decimal(
        state_document["last_nav"], field="checkpoint state.last_nav"
    ):
        raise BacktestCheckpointError(f"checkpoint {path} last_nav does not match the final NAV row")
    ledger_sequences = [row.get("sequence") for row in ledger]
    if any(not isinstance(sequence, int) or isinstance(sequence, bool) for sequence in ledger_sequences):
        raise BacktestCheckpointError("checkpoint state.ledger contains an invalid sequence")
    if ledger_sequences and next_ledger_sequence != max(ledger_sequences) + 1:
        raise BacktestCheckpointError("checkpoint state.next_ledger_sequence does not follow the ledger")

    state = _State(cash=cash)
    state.quantities = defaultdict(Decimal, quantities)
    state.security_currency = dict(security_currency)
    state.orders = [dict(row) for row in orders]
    state.fills = [dict(row) for row in fills]
    state.ledger = [dict(row) for row in ledger]
    state.pending = pending
    state.processed_actions = set(processed_actions)
    state.hold_weights = hold_weights
    state.hold_initialized = hold_initialized
    state.last_decision_date = last_decision_date
    state.last_nav = _checkpoint_decimal(state_document["last_nav"], field="checkpoint state.last_nav")
    state.benchmark_units = optional_decimal("benchmark_units")
    state.benchmark_value_start = optional_decimal("benchmark_value_start")
    state.next_ledger_sequence = next_ledger_sequence
    state.total_fee_base = _checkpoint_decimal(state_document["total_fee_base"], field="checkpoint state.total_fee_base")
    state.total_spread_base = _checkpoint_decimal(state_document["total_spread_base"], field="checkpoint state.total_spread_base")
    state.total_slippage_base = _checkpoint_decimal(
        state_document["total_slippage_base"], field="checkpoint state.total_slippage_base"
    )
    state.total_tax_base = _checkpoint_decimal(state_document["total_tax_base"], field="checkpoint state.total_tax_base")
    state.total_gross_base = _checkpoint_decimal(
        state_document["total_gross_base"], field="checkpoint state.total_gross_base"
    )
    state.cost_by_session = defaultdict(Decimal, cost_by_session)
    state.rejected_trade_count = state_document["rejected_trade_count"]
    state.out_of_market_sessions = state_document["out_of_market_sessions"]
    return state, [dict(row) for row in nav_rows], [dict(row) for row in holding_rows], next_index


def _d(value: Decimal | str | int) -> Decimal:
    if isinstance(value, Decimal):
        result = value
    else:
        try:
            result = Decimal(value)
        except (InvalidOperation, ValueError) as error:
            raise BacktestAccountingError(f"invalid decimal value {value!r}") from error
    if not result.is_finite():
        raise BacktestAccountingError("non-finite decimal is not permitted")
    return result


def _dstr(value: Decimal | str | int) -> str:
    result = _d(value)
    if result == 0:
        return "0"
    text = format(result, "f")
    if "." in text:
        text = text.rstrip("0").rstrip(".")
    return text


def _timestamp(value: datetime) -> str:
    parsed = value.astimezone(UTC)
    fraction = f".{parsed.microsecond:06d}".rstrip("0") if parsed.microsecond else ""
    return parsed.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"


def _uuid(namespace: UUID, name: str) -> str:
    return str(uuid5(namespace, name))


def _date_partition(spec: Mapping[str, Any], session_date: date) -> dict[str, str]:
    for partition in spec["partitions"]:
        if partition["start_date"] <= session_date.isoformat() <= partition["end_date"]:
            return partition
    raise BacktestError(f"session {session_date} is outside all experiment partitions")


def _price_row(inputs: BacktestInputs, security_id: str, session_date: date, as_of: datetime) -> dict[str, Any] | None:
    for row in inputs.prices:
        if row["security_id"] == security_id and row["session_date"] == session_date:
            if row["available_at"] <= as_of:
                return row
            return None
    return None


def _membership_state(
    inputs: BacktestInputs,
    security_id: str,
    session_date: date,
    decision_at: datetime,
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
    leaders = [
        row
        for row in candidates
        if (row["available_at"], row["revision"])
        == (candidates[-1]["available_at"], candidates[-1]["revision"])
    ]
    if len({row["member"] for row in leaders}) != 1:
        raise BacktestInputError(
            f"membership has conflicting equal-ranked assertions for {security_id} on {session_date}"
        )
    return leaders[-1]["member"]


def _fx_rate(inputs: BacktestInputs, source: str, target: str, as_of: datetime) -> Decimal:
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
    candidates: list[tuple[datetime, Decimal]] = [
        (row["fixing_at"], row["rate"]) for row in direct
    ] + [
        (row["fixing_at"], _ONE / row["rate"]) for row in inverse
    ]
    if not candidates:
        raise BacktestMissingDataError(
            f"no point-in-time FX rate supports {source}->{target} at {_timestamp(as_of)}"
        )
    candidates.sort(key=lambda item: item[0])
    latest_at = candidates[-1][0]
    latest = [rate for fixing_at, rate in candidates if fixing_at == latest_at]
    if len({rate for rate in latest}) != 1:
        raise BacktestInputError(f"conflicting FX rates support {source}->{target} at {latest_at}")
    rate = latest[0]
    if rate <= 0:
        raise BacktestInputError("FX rates must be positive")
    return rate


def _convert(inputs: BacktestInputs, amount: Decimal, source: str, target: str, as_of: datetime) -> Decimal:
    with localcontext() as context:
        context.prec = _PRECISION
        return amount * _fx_rate(inputs, source, target, as_of)


def _calendar_sessions(spec: Mapping[str, Any], inputs: BacktestInputs) -> tuple[dict[str, Any], ...]:
    start = date.fromisoformat(spec["period"]["start_date"])
    end = date.fromisoformat(spec["period"]["end_date"])
    sessions = tuple(row for row in inputs.calendar if start <= row["session_date"] <= end)
    if not sessions:
        raise BacktestMissingDataError("calendar has no sessions inside the experiment period")
    for row in sessions:
        if row["available_at"] > row["open_at"]:
            raise BacktestMissingDataError(
                f"calendar session {row['session_date']} is not available by its open"
            )
    covered_kinds = {
        _date_partition(spec, session["session_date"])["kind"]
        for session in sessions
    }
    missing_kinds = [
        partition_kind
        for partition_kind in ("development", "validation", "holdout")
        if partition_kind not in covered_kinds
    ]
    if missing_kinds:
        raise BacktestMissingDataError(
            f"experiment partitions have no exchange sessions: {', '.join(missing_kinds)}"
        )
    return sessions


def _active_members(
    spec: Mapping[str, Any], inputs: BacktestInputs, session: Mapping[str, Any]
) -> tuple[str, ...]:
    active: list[str] = []
    for security_id in spec["universe"]["security_ids"]:
        state = _membership_state(inputs, security_id, session["session_date"], session["close_at"])
        if state is None:
            raise BacktestMissingDataError(
                f"historical membership is unavailable for {security_id} on {session['session_date']}"
            )
        if state and _price_row(inputs, security_id, session["session_date"], session["close_at"]):
            active.append(security_id)
    return tuple(sorted(active))


def _momentum_targets(
    spec: Mapping[str, Any],
    inputs: BacktestInputs,
    sessions: tuple[dict[str, Any], ...],
    index: int,
    active: set[str],
) -> dict[str, Decimal]:
    decision_at = sessions[index]["close_at"]
    history = {
        security_id: tuple(
            (
                row["close"]
                if (row := _price_row(inputs, security_id, sessions[history_index]["session_date"], decision_at))
                is not None
                else None
            )
            for history_index in range(index + 1)
        )
        for security_id in sorted(active)
    }
    return baseline_targets(
        "momentum_12_1",
        security_ids=sorted(active),
        price_history=history,
        session_index=index,
        parameters=spec["strategy"]["parameters"],
    )


def _rebalance_due(spec: Mapping[str, Any], session: Mapping[str, Any], previous: date | None) -> bool:
    if previous is None:
        return True
    name = spec["strategy"]["name"]
    if name == "buy_and_hold":
        return False
    frequency = spec["strategy"]["parameters"].get("rebalance_frequency", "daily")
    return frequency == "daily" or session["session_date"].month != previous.month


def _target_weights(
    spec: Mapping[str, Any], inputs: BacktestInputs, sessions: tuple[dict[str, Any], ...], index: int, state: _State
) -> dict[str, Decimal]:
    session = sessions[index]
    active = set(_active_members(spec, inputs, session))
    name = spec["strategy"]["name"]
    due = _rebalance_due(spec, session, state.last_decision_date)
    initializing_hold = name == "buy_and_hold" and not state.hold_initialized
    if name == "buy_and_hold":
        if initializing_hold:
            selected = sorted(active)
            state.hold_weights = {
                security_id: _ONE / Decimal(len(selected)) for security_id in selected
            } if selected else {}
            state.hold_initialized = True
            targets = dict(state.hold_weights)
        else:
            targets = {}
    elif due:
        if name == "momentum_12_1":
            targets = _momentum_targets(spec, inputs, sessions, index, active)
        else:
            targets = baseline_targets("equal_weight", security_ids=sorted(active))
    else:
        targets = {}
    for security_id, quantity in state.quantities.items():
        if quantity > 0 and security_id not in active:
            targets[security_id] = _ZERO
        elif quantity > 0 and not due and not initializing_hold:
            row = _price_row(inputs, security_id, session["session_date"], session["close_at"])
            if row is None:
                raise BacktestMissingDataError(
                    f"missing close for held security {security_id} on {session['session_date']}"
                )
            current_value = _convert(
                inputs,
                quantity * row["close"],
                row["currency"],
                spec["accounting_policy"]["base_currency"],
                session["close_at"],
            )
            targets[security_id] = current_value / state.last_nav
    if due or name == "buy_and_hold" or any(value == 0 for value in targets.values()):
        max_position = _d(spec["risk_policy"]["max_position_weight"])
        max_gross = _d(spec["risk_policy"]["max_gross_exposure"])
        if any(weight < 0 or weight > max_position for weight in targets.values()):
            raise BacktestError("strategy target exceeds max_position_weight")
        if sum((weight for weight in targets.values()), _ZERO) > max_gross:
            raise BacktestError("strategy target exceeds max_gross_exposure")
    state.last_decision_date = session["session_date"]
    return targets


def _order_quantity(
    spec: Mapping[str, Any],
    inputs: BacktestInputs,
    state: _State,
    security_id: str,
    target_weight: Decimal,
    session: Mapping[str, Any],
) -> tuple[Decimal, str] | None:
    row = _price_row(inputs, security_id, session["session_date"], session["close_at"])
    current = state.quantities.get(security_id, _ZERO)
    if row is None:
        if current > 0:
            raise BacktestMissingDataError(
                f"no point-in-time close supports an exit decision for {security_id} on {session['session_date']}"
            )
        return None
    state.security_currency[security_id] = row["currency"]
    target_value_base = state.last_nav * target_weight
    target_value_local = _convert(
        inputs,
        target_value_base,
        spec["accounting_policy"]["base_currency"],
        row["currency"],
        session["close_at"],
    )
    target_quantity = target_value_local / row["close"]
    if not spec["accounting_policy"]["fractional_shares"]:
        target_quantity = target_quantity.to_integral_value(rounding=ROUND_DOWN)
    delta = target_quantity - current
    if delta == 0:
        return None
    return abs(delta), "buy" if delta > 0 else "sell"


def _ledger(
    state: _State,
    *,
    session_date: date,
    event_at: datetime,
    event_type: str,
    currency: str,
    amount_local: Decimal,
    amount_base: Decimal,
    note: str,
    security_id: str | None = None,
    quantity: Decimal | None = None,
    price: Decimal | None = None,
) -> None:
    row: dict[str, Any] = {
        "sequence": state.next_ledger_sequence,
        "session_date": session_date.isoformat(),
        "event_at": _timestamp(event_at),
        "event_type": event_type,
        "currency": currency,
        "amount_local": _dstr(amount_local),
        "amount_base": _dstr(amount_base),
        "note": note,
    }
    if security_id is not None:
        row["security_id"] = security_id
    if quantity is not None:
        row["quantity"] = _dstr(quantity)
    if price is not None:
        row["price"] = _dstr(price)
    state.ledger.append(row)
    state.next_ledger_sequence += 1


def _action_events(inputs: BacktestInputs, session_date: date) -> list[tuple[int, dict[str, Any]]]:
    events: list[tuple[int, dict[str, Any]]] = []
    for action in inputs.corporate_actions:
        if action["action_type"] == "cash_dividend":
            if action["payment_date"] == session_date:
                events.append((1, action))
        elif action["effective_date"] == session_date:
            events.append((0 if action["action_type"] == "split" else 2, action))
    events.sort(key=lambda item: (item[0], item[1]["id"]))
    return events


def _apply_actions(
    spec: Mapping[str, Any], inputs: BacktestInputs, state: _State, session: Mapping[str, Any]
) -> None:
    for _, action in _action_events(inputs, session["session_date"]):
        action_id = action["id"]
        if action_id in state.processed_actions:
            continue
        security_id = action["security_id"]
        quantity = state.quantities.get(security_id, _ZERO)
        if action["available_at"] > session["open_at"]:
            if quantity > 0:
                raise BacktestMissingDataError(
                    f"corporate action {action_id} is not available by its economic event"
                )
            state.processed_actions.add(action_id)
            continue
        currency = action["currency"]
        if quantity > 0:
            known_currency = state.security_currency.get(security_id)
            if known_currency is None:
                price = _price_row(inputs, security_id, session["session_date"], session["open_at"])
                known_currency = price["currency"] if price else currency
                state.security_currency[security_id] = known_currency
            if known_currency != currency:
                raise BacktestInputError(f"action {action_id} currency disagrees with security price currency")
        if action["action_type"] == "split":
            if quantity > 0:
                factor = action["ratio_numerator"] / action["ratio_denominator"]
                delta = quantity * (factor - _ONE)
                state.quantities[security_id] = quantity * factor
                _ledger(
                    state,
                    session_date=session["session_date"],
                    event_at=session["open_at"],
                    event_type="split",
                    currency=currency,
                    amount_local=_ZERO,
                    amount_base=_ZERO,
                    note=f"split {action['ratio_numerator']}:{action['ratio_denominator']}",
                    security_id=security_id,
                    quantity=delta,
                )
        elif action["action_type"] == "cash_dividend":
            if quantity > 0:
                amount = quantity * action["cash_amount"]
                state.cash[currency] = state.cash.get(currency, _ZERO) + amount
                amount_base = _convert(
                    inputs, amount, currency, spec["accounting_policy"]["base_currency"], session["open_at"]
                )
                _ledger(
                    state,
                    session_date=session["session_date"],
                    event_at=session["open_at"],
                    event_type="dividend",
                    currency=currency,
                    amount_local=amount,
                    amount_base=amount_base,
                    note=f"cash dividend {action_id}",
                    security_id=security_id,
                    quantity=quantity,
                    price=action["cash_amount"],
                )
        elif action["action_type"] == "delisting" and quantity > 0:
            settlement = action["settlement_price"]
            if settlement is None:
                raise BacktestMissingDataError(f"delisting {action_id} has no settlement price")
            amount = quantity * settlement
            state.cash[currency] = state.cash.get(currency, _ZERO) + amount
            amount_base = _convert(
                inputs, amount, currency, spec["accounting_policy"]["base_currency"], session["open_at"]
            )
            state.quantities[security_id] = _ZERO
            _ledger(
                state,
                session_date=session["session_date"],
                event_at=session["open_at"],
                event_type="delisting",
                currency=currency,
                amount_local=amount,
                amount_base=amount_base,
                note=f"delisting settlement {action_id}",
                security_id=security_id,
                quantity=-quantity,
                price=settlement,
            )
        state.processed_actions.add(action_id)


def _costs(spec: Mapping[str, Any], gross: Decimal) -> tuple[Decimal, Decimal, Decimal, Decimal]:
    policy = spec["cost_policy"]
    commission = gross * _d(policy["commission_bps"]) / _BPS + _d(policy["fixed_fee"])
    commission = max(commission, _d(policy["minimum_fee"]))
    spread = gross * _d(policy["spread_bps"]) / (_BPS * 2)
    slippage = gross * _d(policy["slippage_bps"]) / _BPS
    tax = gross * _d(policy["tax_bps"]) / _BPS
    return commission, spread, slippage, tax


def _fill_terms(
    spec: Mapping[str, Any], row: Mapping[str, Any], side: str, quantity: Decimal
) -> tuple[Decimal, Decimal, Decimal, Decimal, Decimal, Decimal]:
    commission, spread, slippage, tax = _costs(spec, row["open"] * quantity)
    spread_rate = _d(spec["cost_policy"]["spread_bps"]) / (_BPS * 2)
    slippage_rate = _d(spec["cost_policy"]["slippage_bps"]) / _BPS
    direction = _ONE if side == "buy" else -_ONE
    fill_price = row["open"] * (_ONE + direction * (spread_rate + slippage_rate))
    trade_notional = fill_price * quantity
    return fill_price, trade_notional, commission, spread, slippage, tax


def _max_affordable_quantity(
    spec: Mapping[str, Any],
    row: Mapping[str, Any],
    quantity: Decimal,
    available_local: Decimal,
) -> Decimal:
    """Find the largest fractional buy that fits after fees and tax."""

    low = _ZERO
    high = quantity
    for _ in range(120):
        middle = (low + high) / 2
        _, trade_notional, commission, _, _, tax = _fill_terms(spec, row, "buy", middle)
        if trade_notional + commission + tax <= available_local:
            low = middle
        else:
            high = middle
    if not spec["accounting_policy"]["fractional_shares"]:
        low = low.to_integral_value(rounding=ROUND_DOWN)
    return low


def _fund_trade_currency(
    spec: Mapping[str, Any],
    inputs: BacktestInputs,
    state: _State,
    *,
    currency: str,
    required_local: Decimal,
    event_at: datetime,
    session_date: date,
) -> bool:
    """Exchange base cash for a foreign-currency buy and retain both audit legs."""

    base_currency = spec["accounting_policy"]["base_currency"]
    available_local = state.cash.get(currency, _ZERO)
    shortfall = required_local - available_local
    if shortfall <= 0:
        return True
    if currency == base_currency:
        return False
    required_base = _convert(inputs, shortfall, currency, base_currency, event_at)
    if state.cash.get(base_currency, _ZERO) < required_base:
        return False
    state.cash[currency] = available_local + shortfall
    state.cash[base_currency] = state.cash.get(base_currency, _ZERO) - required_base
    _ledger(
        state,
        session_date=session_date,
        event_at=event_at,
        event_type="fx_conversion",
        currency=base_currency,
        amount_local=-required_base,
        amount_base=-required_base,
        note=f"fund {currency} buy",
    )
    _ledger(
        state,
        session_date=session_date,
        event_at=event_at,
        event_type="fx_conversion",
        currency=currency,
        amount_local=shortfall,
        amount_base=_convert(inputs, shortfall, currency, base_currency, event_at),
        note=f"fund {currency} buy",
    )
    return True


def _reject_order(
    spec: Mapping[str, Any], state: _State, order: _PendingOrder, session: Mapping[str, Any], reason: str
) -> None:
    state.orders[order.index]["status"] = "rejected"
    state.rejected_trade_count += 1
    _ledger(
        state,
        session_date=session["session_date"],
        event_at=session["open_at"],
        event_type="reject",
        currency=order.currency,
        amount_local=_ZERO,
        amount_base=_ZERO,
        note=reason,
        security_id=order.security_id,
        quantity=order.quantity if order.side == "buy" else -order.quantity,
    )
    if spec["missing_data_policy"] == "halt_experiment":
        raise BacktestMissingDataError(reason)


def _execute_orders(
    spec: Mapping[str, Any], inputs: BacktestInputs, state: _State, session: Mapping[str, Any]
) -> None:
    pending = [order for order in state.pending if order.execution_date == session["session_date"]]
    state.pending = [order for order in state.pending if order.execution_date != session["session_date"]]
    for order in sorted(pending, key=lambda item: item.index):
        row = _price_row(inputs, order.security_id, session["session_date"], session["open_at"])
        if row is None:
            _reject_order(spec, state, order, session, f"missing executable open for {order.security_id}")
            continue
        if row["currency"] != order.currency:
            raise BacktestInputError(f"order currency disagrees with execution price for {order.security_id}")
        quantity = order.quantity
        if order.side == "sell":
            quantity = min(quantity, state.quantities.get(order.security_id, _ZERO))
            if quantity <= 0:
                _reject_order(spec, state, order, session, f"no position available to sell {order.security_id}")
                continue
        max_participation = _d(spec["risk_policy"]["max_participation"])
        if row["has_volume"]:
            if quantity > row["volume"] * max_participation:
                _reject_order(spec, state, order, session, f"participation limit rejects {order.security_id}")
                continue
        elif max_participation < _ONE:
            _reject_order(spec, state, order, session, f"volume evidence missing for {order.security_id}")
            continue
        if order.side == "buy":
            _, estimated_trade_notional, estimated_commission, _, _, estimated_tax = _fill_terms(
                spec, row, order.side, quantity
            )
            available_local = state.cash.get(order.currency, _ZERO)
            base_currency = spec["accounting_policy"]["base_currency"]
            if order.currency != base_currency:
                available_local += _convert(
                    inputs,
                    state.cash.get(base_currency, _ZERO),
                    base_currency,
                    order.currency,
                    session["open_at"],
                )
            if estimated_trade_notional + estimated_commission + estimated_tax > available_local:
                quantity = _max_affordable_quantity(spec, row, quantity, available_local)
                if quantity <= 0:
                    _reject_order(spec, state, order, session, f"insufficient {order.currency} cash for {order.security_id}")
                    continue
        fill_price, trade_notional, commission, spread, slippage, tax = _fill_terms(
            spec, row, order.side, quantity
        )
        local_cash_change = trade_notional + commission + tax
        if order.side == "buy" and not _fund_trade_currency(
            spec,
            inputs,
            state,
            currency=order.currency,
            required_local=local_cash_change,
            event_at=session["open_at"],
            session_date=session["session_date"],
        ):
            _reject_order(spec, state, order, session, f"insufficient {order.currency} cash for {order.security_id}")
            continue
        gross = row["open"] * quantity
        if order.side == "buy":
            state.cash[order.currency] = state.cash.get(order.currency, _ZERO) - local_cash_change
            state.quantities[order.security_id] = state.quantities.get(order.security_id, _ZERO) + quantity
        else:
            state.cash[order.currency] = state.cash.get(order.currency, _ZERO) + trade_notional - commission - tax
            state.quantities[order.security_id] = max(
                _ZERO, state.quantities.get(order.security_id, _ZERO) - quantity
            )
        state.orders[order.index]["status"] = "filled"
        state.orders[order.index]["quantity"] = _dstr(quantity)
        execution_at = session["open_at"]
        base_currency = spec["accounting_policy"]["base_currency"]
        gross_base = _convert(inputs, gross, order.currency, base_currency, execution_at)
        fee_base = _convert(inputs, commission, order.currency, base_currency, execution_at)
        spread_base = _convert(inputs, spread, order.currency, base_currency, execution_at)
        slippage_base = _convert(inputs, slippage, order.currency, base_currency, execution_at)
        tax_base = _convert(inputs, tax, order.currency, base_currency, execution_at)
        fill_id = _uuid(
            _FILL_NAMESPACE,
            f"{state.orders[order.index]['order_id']}:{_dstr(quantity)}:{_dstr(fill_price)}",
        )
        state.fills.append(
            {
                "fill_id": fill_id,
                "order_id": state.orders[order.index]["order_id"],
                "execution_at": _timestamp(execution_at),
                "execution_session": session["session_date"].isoformat(),
                "security_id": order.security_id,
                "side": order.side,
                "quantity": _dstr(quantity),
                "reference_price": _dstr(row["open"]),
                "fill_price": _dstr(fill_price),
                "gross_notional": _dstr(gross),
                "fee": _dstr(commission),
                "spread_cost": _dstr(spread),
                "slippage_cost": _dstr(slippage),
                "tax": _dstr(tax),
                "currency": order.currency,
                "base_notional": _dstr(gross_base),
                "status": "filled",
            }
        )
        sign = _ONE if order.side == "sell" else -_ONE
        _ledger(
            state,
            session_date=session["session_date"],
            event_at=execution_at,
            event_type="fill",
            currency=order.currency,
            amount_local=sign * trade_notional,
            amount_base=sign * _convert(inputs, trade_notional, order.currency, base_currency, execution_at),
            note=f"{order.side} {order.security_id}",
            security_id=order.security_id,
            quantity=quantity if order.side == "buy" else -quantity,
            price=fill_price,
        )
        _ledger(
            state,
            session_date=session["session_date"],
            event_at=execution_at,
            event_type="fee",
            currency=order.currency,
            amount_local=-commission,
            amount_base=-fee_base,
            note=f"commission {order.security_id}",
            security_id=order.security_id,
        )
        _ledger(
            state,
            session_date=session["session_date"],
            event_at=execution_at,
            event_type="tax",
            currency=order.currency,
            amount_local=-tax,
            amount_base=-tax_base,
            note=f"tax {order.security_id}",
            security_id=order.security_id,
        )
        state.total_fee_base += fee_base
        state.total_spread_base += spread_base
        state.total_slippage_base += slippage_base
        state.total_tax_base += tax_base
        state.total_gross_base += gross_base
        state.cost_by_session[session["session_date"]] += fee_base + spread_base + slippage_base + tax_base


def _mark(
    spec: Mapping[str, Any], inputs: BacktestInputs, state: _State, session: Mapping[str, Any]
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    base_currency = spec["accounting_policy"]["base_currency"]
    cash_by_currency = {
        currency: amount for currency, amount in sorted(state.cash.items()) if amount != 0
    }
    cash_base = sum(
        (_convert(inputs, amount, currency, base_currency, session["close_at"])
         for currency, amount in cash_by_currency.items()),
        _ZERO,
    )
    holdings: list[dict[str, Any]] = []
    positions_base = _ZERO
    for security_id in sorted(state.quantities):
        quantity = state.quantities[security_id]
        if quantity <= 0:
            continue
        row = _price_row(inputs, security_id, session["session_date"], session["close_at"])
        if row is None:
            raise BacktestMissingDataError(
                f"missing point-in-time close for held security {security_id} on {session['session_date']}"
            )
        state.security_currency[security_id] = row["currency"]
        local_value = quantity * row["close"]
        base_value = _convert(inputs, local_value, row["currency"], base_currency, session["close_at"])
        positions_base += base_value
        holdings.append(
            {
                "session_date": session["session_date"].isoformat(),
                "security_id": security_id,
                "quantity": _dstr(quantity),
                "price": _dstr(row["close"]),
                "currency": row["currency"],
                "market_value_local": _dstr(local_value),
                "market_value_base": _dstr(base_value),
                "position_weight": _dstr(base_value / max(cash_base + positions_base, _ONE)),
            }
        )
    nav = cash_base + positions_base
    if nav <= 0:
        raise BacktestAccountingError(f"NAV is not positive on {session['session_date']}")
    for holding in holdings:
        holding["position_weight"] = _dstr(_d(holding["market_value_base"]) / nav)
    benchmark_row = _price_row(
        inputs,
        spec["benchmark"]["security_id"],
        session["session_date"],
        session["close_at"],
    )
    if benchmark_row is None:
        raise BacktestMissingDataError(
            f"missing benchmark close on {session['session_date']}"
        )
    benchmark_value = _convert(
        inputs,
        benchmark_row["close"],
        benchmark_row["currency"],
        base_currency,
        session["close_at"],
    )
    if state.benchmark_value_start is None:
        state.benchmark_value_start = benchmark_value
        state.benchmark_units = _d(spec["accounting_policy"]["initial_cash"]) / benchmark_value
    benchmark_nav = benchmark_value * (state.benchmark_units or _ZERO)
    reporting_currency = spec["accounting_policy"]["reporting_currency"]
    reporting_nav = (
        _convert(inputs, nav, base_currency, reporting_currency, session["close_at"])
        if reporting_currency is not None
        else None
    )
    partition = _date_partition(spec, session["session_date"])
    nav_row = {
        "session_date": session["session_date"].isoformat(),
        "partition": partition["name"],
        "decision_at": _timestamp(session["close_at"]),
        "cash_by_currency": {currency: _dstr(amount) for currency, amount in cash_by_currency.items()},
        "cash_base": _dstr(cash_base),
        "positions_value_base": _dstr(positions_base),
        "nav": _dstr(nav),
        "gross_exposure": _dstr(positions_base / nav),
        "net_exposure": _dstr(positions_base / nav),
        "benchmark_nav": _dstr(benchmark_nav),
        "reporting_nav": _dstr(reporting_nav) if reporting_nav is not None else None,
    }
    _ledger(
        state,
        session_date=session["session_date"],
        event_at=session["close_at"],
        event_type="mark",
        currency=base_currency,
        amount_local=positions_base,
        amount_base=positions_base,
        note="close mark",
    )
    state.last_nav = nav
    if positions_base == 0:
        state.out_of_market_sessions += 1
    return nav_row, holdings


def _returns(values: Iterable[Decimal]) -> list[Decimal]:
    series = list(values)
    return [current / previous - _ONE for previous, current in pairwise(series)]


def _safe_metric(value: Decimal | None) -> str | None:
    return _dstr(value) if value is not None and value.is_finite() else None


def _attribution(
    nav_rows: tuple[dict[str, Any], ...],
    state: _State,
    fills: Iterable[dict[str, Any]],
    key: Any,
) -> dict[str, dict[str, Any]]:
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in nav_rows:
        grouped[key(row)].append(row)
    fill_dates = [date.fromisoformat(row["execution_session"]) for row in fills]
    result: dict[str, dict[str, Any]] = {}
    for label, rows in sorted(grouped.items()):
        dates = {date.fromisoformat(row["session_date"]) for row in rows}
        start_nav = _d(rows[0]["nav"])
        end_nav = _d(rows[-1]["nav"])
        cost = sum((state.cost_by_session[item] for item in sorted(dates)), _ZERO)
        result[label] = {
            "start_nav": _dstr(start_nav),
            "end_nav": _dstr(end_nav),
            "return": _dstr(end_nav / start_nav - _ONE),
            "trade_count": sum(item in dates for item in fill_dates),
            "cost": _dstr(cost),
        }
    return result


def _risk_free_series(
    spec: Mapping[str, Any], inputs: BacktestInputs, nav_rows: tuple[dict[str, Any], ...]
) -> list[dict[str, str]]:
    policy = spec["metrics_policy"]
    annualization_factor = Decimal(policy["annualization_factor"])
    result: list[dict[str, str]] = []
    for previous, current in pairwise(nav_rows):
        end_at = datetime.fromisoformat(current["decision_at"])
        if policy["risk_free_source"] == "constant_annual":
            annual_rate = _d(policy["risk_free_annual"])
        else:
            candidates = [
                row
                for row in inputs.risk_free
                if row["observed_at"] <= end_at and row["available_at"] <= end_at
            ]
            if not candidates:
                raise BacktestMissingDataError(
                    f"no point-in-time risk-free observation supports {current['session_date']}"
                )
            candidates.sort(key=lambda row: (row["observed_at"], row["available_at"], row["revision"]))
            latest_rank = (
                candidates[-1]["observed_at"],
                candidates[-1]["available_at"],
                candidates[-1]["revision"],
            )
            latest = [
                row
                for row in candidates
                if (row["observed_at"], row["available_at"], row["revision"]) == latest_rank
            ]
            if len({row["value"] for row in latest}) != 1:
                raise BacktestInputError(
                    f"risk-free observations conflict at {current['session_date']}"
                )
            annual_rate = latest[-1]["value"]
        with localcontext() as context:
            context.prec = _PRECISION
            daily_rate = annual_rate / annualization_factor
        result.append(
            {
                "period_start": previous["session_date"],
                "period_end": current["session_date"],
                "annual_rate": _dstr(annual_rate),
                "daily_rate": _dstr(daily_rate),
            }
        )
    return result


def _metrics(
    spec: Mapping[str, Any],
    inputs: BacktestInputs,
    state: _State,
    nav_rows: tuple[dict[str, Any], ...],
) -> dict[str, Any]:
    metrics_policy = spec["metrics_policy"]
    annualization_factor = metrics_policy["annualization_factor"]
    nav_values = [_d(row["nav"]) for row in nav_rows]
    benchmark_values = [_d(row["benchmark_nav"]) for row in nav_rows]
    returns = _returns(nav_values)
    benchmark_returns = _returns(benchmark_values)
    risk_free_series = _risk_free_series(spec, inputs, nav_rows)
    risk_free_daily = [_d(row["daily_rate"]) for row in risk_free_series]
    total_return = nav_values[-1] / nav_values[0] - _ONE
    benchmark_return = benchmark_values[-1] / benchmark_values[0] - _ONE
    with localcontext() as context:
        context.prec = _PRECISION
        cagr = (
            ((nav_values[-1] / nav_values[0]).ln() * (Decimal(annualization_factor) / Decimal(max(len(nav_values) - 1, 1)))).exp()
            - _ONE
        )
    mean = sum(returns, _ZERO) / Decimal(len(returns)) if returns else None
    variance = (
        sum(((value - mean) ** 2 for value in returns), _ZERO) / Decimal(len(returns) - 1)
        if mean is not None and len(returns) > 1
        else None
    )
    volatility = variance.sqrt() * Decimal(annualization_factor).sqrt() if variance is not None else None
    excess = [value - rate for value, rate in zip(returns, risk_free_daily)]
    excess_mean = sum(excess, _ZERO) / Decimal(len(excess)) if excess else None
    sharpe = excess_mean * Decimal(annualization_factor).sqrt() / volatility if volatility else None
    downside = [min(value - rate, _ZERO) ** 2 for value, rate in zip(returns, risk_free_daily)]
    downside_deviation = (
        (sum(downside, _ZERO) / Decimal(len(downside))).sqrt() * Decimal(annualization_factor).sqrt()
        if downside
        else None
    )
    sortino = excess_mean * Decimal(annualization_factor).sqrt() / downside_deviation if downside_deviation else None
    peak = nav_values[0]
    max_drawdown = _ZERO
    for value in nav_values:
        peak = max(peak, value)
        max_drawdown = min(max_drawdown, value / peak - _ONE)
    beta = None
    alpha = None
    if len(returns) > 1 and len(returns) == len(benchmark_returns):
        benchmark_mean = sum(benchmark_returns, _ZERO) / Decimal(len(benchmark_returns))
        covariance = sum(
            ((left - mean) * (right - benchmark_mean) for left, right in zip(returns, benchmark_returns)),
            _ZERO,
        ) / Decimal(len(returns) - 1)
        benchmark_variance = sum(
            ((right - benchmark_mean) ** 2 for right in benchmark_returns), _ZERO
        ) / Decimal(len(benchmark_returns) - 1)
        if benchmark_variance:
            beta = covariance / benchmark_variance
            alpha = mean - beta * benchmark_mean
    total_cost = state.total_fee_base + state.total_spread_base + state.total_slippage_base + state.total_tax_base
    values = {
        "total_return": _safe_metric(total_return),
        "cagr": _safe_metric(cagr),
        "annualized_volatility": _safe_metric(volatility),
        "sharpe": _safe_metric(sharpe),
        "sortino": _safe_metric(sortino),
        "max_drawdown": _safe_metric(max_drawdown),
        "calmar": _safe_metric(total_return / abs(max_drawdown)) if max_drawdown else None,
        "beta": _safe_metric(beta),
        "alpha": _safe_metric(alpha),
        "turnover": _safe_metric(state.total_gross_base / max(nav_values[0], _ONE)),
        "win_rate": _safe_metric(Decimal(sum(value > 0 for value in returns)) / Decimal(len(returns))) if returns else None,
        "profit_factor": None,
        "benchmark_relative_return": _safe_metric(total_return - benchmark_return),
        "total_cost": _safe_metric(total_cost),
        "fee_cost": _safe_metric(state.total_fee_base),
        "spread_cost": _safe_metric(state.total_spread_base),
        "slippage_cost": _safe_metric(state.total_slippage_base),
        "tax_cost": _safe_metric(state.total_tax_base),
        "fx_cost": "0",
        "rejected_trade_count": str(state.rejected_trade_count),
        "out_of_market_sessions": str(state.out_of_market_sessions),
    }
    return {
        "schema_version": BACKTEST_SCHEMA_VERSION,
        "metrics_version": METRICS_VERSION,
        "base_currency": spec["accounting_policy"]["base_currency"],
        "annualization_factor": annualization_factor,
        "risk_free_annual": metrics_policy["risk_free_annual"],
        "metrics_policy": metrics_policy,
        "risk_free_series": risk_free_series,
        "values": values,
        "attribution": {
            "by_partition": _attribution(
                nav_rows,
                state,
                state.fills,
                lambda row: row["partition"],
            ),
            "by_year": _attribution(
                nav_rows,
                state,
                state.fills,
                lambda row: row["session_date"][:4],
            ),
        },
    }


def simulate_backtest(
    spec: Mapping[str, Any],
    *,
    data_root: str | Path,
    checkpoint_root: str | Path | None = None,
    resume: bool = False,
    stop_after_session: int | None = None,
) -> BacktestRun:
    """Run one deterministic daily experiment from hash-pinned local inputs.

    When ``checkpoint_root`` is supplied, the engine appends one authenticated
    checkpoint per completed session. ``resume=True`` restores the latest
    checkpoint and continues from its next session without duplicating ledger,
    order, fill, or valuation rows.
    """

    normalized = validate_experiment_spec(spec)
    spec_hash = experiment_sha256(normalized)
    input_hash = input_fingerprint(normalized)
    inputs = load_backtest_inputs(normalized, data_root=data_root)
    sessions = _calendar_sessions(normalized, inputs)
    if stop_after_session is not None:
        if (
            not isinstance(stop_after_session, int)
            or isinstance(stop_after_session, bool)
            or stop_after_session < 1
            or stop_after_session >= len(sessions)
        ):
            raise BacktestCheckpointError(
                "stop_after_session must be a positive session count before the final session"
            )
        if resume:
            raise BacktestCheckpointError("stop_after_session cannot be combined with resume")
    if resume and checkpoint_root is None:
        raise BacktestCheckpointError("resume requires checkpoint_root")

    checkpoint_directory: Path | None = None
    checkpoint_entry: tuple[Path, int] | None = None
    if checkpoint_root is not None:
        checkpoint_directory = _checkpoint_directory(checkpoint_root, normalized["experiment_id"])
        checkpoint_entry = _latest_checkpoint(checkpoint_directory)
        if not resume and checkpoint_entry is not None:
            raise BacktestCheckpointError(
                f"checkpoint already exists for experiment {normalized['experiment_id']}; use resume"
            )

    if resume:
        assert checkpoint_directory is not None
        if checkpoint_entry is None:
            raise BacktestCheckpointError(
                f"no checkpoint exists for experiment {normalized['experiment_id']}"
            )
        checkpoint_path, checkpoint_index = checkpoint_entry
        state, nav_rows, holding_rows, next_session_index = _restore_checkpoint(
            checkpoint_path,
            expected_experiment_id=normalized["experiment_id"],
            expected_spec_hash=spec_hash,
            expected_input_hash=input_hash,
            expected_engine_version=ENGINE_VERSION,
            sessions=sessions,
            expected_index=checkpoint_index,
        )
    else:
        base_currency = normalized["accounting_policy"]["base_currency"]
        state = _State(cash={base_currency: _d(normalized["accounting_policy"]["initial_cash"])})
        first_session = sessions[0]
        _ledger(
            state,
            session_date=first_session["session_date"],
            event_at=first_session["open_at"],
            event_type="initial_cash",
            currency=base_currency,
            amount_local=state.cash[base_currency],
            amount_base=state.cash[base_currency],
            note="initial cash",
        )
        nav_rows = []
        holding_rows = []
        next_session_index = 0

    session_dates = [session["session_date"] for session in sessions]
    for index in range(next_session_index, len(sessions)):
        session = sessions[index]
        _apply_actions(normalized, inputs, state, session)
        _execute_orders(normalized, inputs, state, session)
        nav_row, holdings = _mark(normalized, inputs, state, session)
        nav_rows.append(nav_row)
        holding_rows.extend(holdings)
        if index + 1 < len(sessions):
            targets = _target_weights(normalized, inputs, sessions, index, state)
            execution_date = session_dates[index + 1]
            candidates = set(targets) | {
                security_id for security_id, quantity in state.quantities.items() if quantity > 0
            }
            for ordinal, security_id in enumerate(sorted(candidates)):
                target_weight = targets.get(security_id, _ZERO)
                member_state = _membership_state(inputs, security_id, session["session_date"], session["close_at"])
                if member_state is None and state.quantities.get(security_id, _ZERO) > 0:
                    raise BacktestMissingDataError(
                        f"historical membership is unavailable for held security {security_id} on {session['session_date']}"
                    )
                quantity_side = _order_quantity(normalized, inputs, state, security_id, target_weight, session)
                if quantity_side is None:
                    continue
                quantity, side = quantity_side
                row = _price_row(inputs, security_id, session["session_date"], session["close_at"])
                assert row is not None
                order_id = _uuid(
                    _ORDER_NAMESPACE,
                    f"{normalized['experiment_id']}:{session['session_date'].isoformat()}:{security_id}:{ordinal}",
                )
                reason = "membership_exit" if target_weight == 0 and member_state is False else normalized["strategy"]["name"]
                order = {
                    "order_id": order_id,
                    "decision_session": session["session_date"].isoformat(),
                    "execution_session": execution_date.isoformat(),
                    "security_id": security_id,
                    "side": side,
                    "quantity": _dstr(quantity),
                    "reference_price": _dstr(row["close"]),
                    "currency": row["currency"],
                    "target_weight": _dstr(target_weight),
                    "reason": reason,
                    "status": "proposed",
                }
                order_index = len(state.orders)
                state.orders.append(order)
                _ledger(
                    state,
                    session_date=session["session_date"],
                    event_at=session["close_at"],
                    event_type="order",
                    currency=row["currency"],
                    amount_local=_ZERO,
                    amount_base=_ZERO,
                    note=f"{side} order {order_id}",
                    security_id=security_id,
                    quantity=quantity if side == "buy" else -quantity,
                    price=row["close"],
                )
                state.pending.append(
                    _PendingOrder(order_index, security_id, side, quantity, execution_date, row["currency"])
                )
        if checkpoint_directory is not None:
            checkpoint_path = checkpoint_directory / f"checkpoint-{index + 1:06d}.json"
            _write_checkpoint(
                checkpoint_path,
                _checkpoint_payload(
                    normalized=normalized,
                    spec_hash=spec_hash,
                    input_hash=input_hash,
                    next_session_index=index + 1,
                    state=state,
                    nav_rows=nav_rows,
                    holding_rows=holding_rows,
                ),
            )
            if stop_after_session == index + 1:
                raise BacktestInterruptedError(
                    f"interrupted after session {session['session_date']}; checkpoint={checkpoint_path}"
                )
    if state.pending:
        raise BacktestError("pending orders remain after the final executable session")
    nav_tuple = tuple(nav_rows)
    metrics = _metrics(normalized, inputs, state, nav_tuple)
    result_id = _uuid(_RESULT_NAMESPACE, f"{normalized['experiment_id']}:{spec_hash}:{input_hash}:{ENGINE_VERSION}")
    summary = {
        "session_count": len(nav_rows),
        "holdings_count": len(holding_rows),
        "order_count": len(state.orders),
        "fill_count": len(state.fills),
        "ledger_count": len(state.ledger),
        "rejected_trade_count": state.rejected_trade_count,
        "holdout_attempts": 0,
    }
    return BacktestRun(
        result_id=result_id,
        experiment_id=normalized["experiment_id"],
        experiment_sha256=spec_hash,
        input_fingerprint=input_hash,
        engine_version=ENGINE_VERSION,
        context={
            "strategy": normalized["strategy"],
            "period": normalized["period"],
            "universe": normalized["universe"],
            "cost_policy": normalized["cost_policy"],
            "metrics_policy": normalized["metrics_policy"],
            "base_currency": normalized["accounting_policy"]["base_currency"],
            "reporting_currency": normalized["accounting_policy"]["reporting_currency"],
        },
        nav=tuple(nav_rows),
        holdings=tuple(holding_rows),
        orders=tuple(state.orders),
        fills=tuple(state.fills),
        ledger=tuple(state.ledger),
        metrics=metrics,
        summary=summary,
    )


__all__ = [
    "ANNUALIZATION_FACTOR",
    "BACKTEST_SCHEMA_VERSION",
    "CHECKPOINT_SCHEMA_VERSION",
    "METRICS_VERSION",
    "BacktestAccountingError",
    "BacktestCheckpointError",
    "BacktestError",
    "BacktestInterruptedError",
    "BacktestMissingDataError",
    "BacktestRun",
    "simulate_backtest",
]
