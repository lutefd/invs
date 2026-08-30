"""Transparent baseline signal and target-weight constructors."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from decimal import Decimal
from typing import Any

_ZERO = Decimal(0)
_ONE = Decimal(1)


class StrategySignalError(ValueError):
    """Raised when a baseline cannot construct a valid target signal."""


def equal_weight_targets(security_ids: Sequence[str]) -> dict[str, Decimal]:
    """Return deterministic equal weights for a sorted or unsorted security set."""

    selected = tuple(sorted(set(security_ids)))
    if not selected:
        return {}
    weight = _ONE / Decimal(len(selected))
    return {security_id: weight for security_id in selected}


def momentum_12_1_targets(
    security_ids: Sequence[str],
    *,
    price_history: Mapping[str, Sequence[Decimal | None]],
    session_index: int,
    lookback_sessions: int,
    skip_sessions: int,
    top_k: int,
) -> dict[str, Decimal]:
    """Rank trailing returns without reading prices after the skipped cutoff."""

    if lookback_sessions < 1 or skip_sessions < 1 or skip_sessions >= lookback_sessions:
        raise StrategySignalError("momentum lookback and skip sessions are invalid")
    if top_k < 1:
        raise StrategySignalError("momentum top_k must be positive")
    end_index = session_index - skip_sessions
    start_index = end_index - lookback_sessions
    if start_index < 0 or end_index < 0:
        return {}
    scores: list[tuple[Decimal, str]] = []
    for security_id in sorted(set(security_ids)):
        history = price_history.get(security_id, ())
        if end_index >= len(history) or start_index >= len(history):
            continue
        start = history[start_index]
        end = history[end_index]
        if start is None or end is None or start <= _ZERO:
            continue
        scores.append((end / start - _ONE, security_id))
    scores.sort(key=lambda item: (-item[0], item[1]))
    return equal_weight_targets([security_id for _, security_id in scores[:top_k]])


def baseline_targets(
    name: str,
    *,
    security_ids: Sequence[str],
    price_history: Mapping[str, Sequence[Decimal | None]] | None = None,
    session_index: int | None = None,
    parameters: Mapping[str, Any] | None = None,
) -> dict[str, Decimal]:
    """Build equal-weight or momentum targets from an immutable signal context."""

    if name == "equal_weight":
        return equal_weight_targets(security_ids)
    if name == "momentum_12_1":
        if price_history is None or session_index is None or parameters is None:
            raise StrategySignalError("momentum requires price history, session index, and parameters")
        return momentum_12_1_targets(
            security_ids,
            price_history=price_history,
            session_index=session_index,
            lookback_sessions=parameters["lookback_sessions"],
            skip_sessions=parameters["skip_sessions"],
            top_k=parameters["top_k"],
        )
    raise StrategySignalError(f"unsupported baseline strategy {name}")


__all__ = [
    "StrategySignalError",
    "baseline_targets",
    "equal_weight_targets",
    "momentum_12_1_targets",
]
