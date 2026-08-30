from __future__ import annotations

import json
import os
from copy import deepcopy
from pathlib import Path

import pytest

from research.data_fitness import DataFitnessError, validate_data_fitness_matrix

REPO_ROOT = Path(os.environ.get("INVS_REPO_ROOT", Path(__file__).resolve().parents[2]))
MATRIX_PATH = REPO_ROOT / "release" / "data-fitness.json"


def _matrix() -> dict:
    return json.loads(MATRIX_PATH.read_text(encoding="utf-8"))


def _write_matrix(tmp_path: Path, matrix: dict) -> Path:
    path = tmp_path / "data-fitness.json"
    path.write_text(json.dumps(matrix), encoding="utf-8")
    return path


def test_checked_in_matrix_classifies_every_reachable_surface() -> None:
    matrix = validate_data_fitness_matrix(MATRIX_PATH, repo_root=REPO_ROOT)
    assert len(matrix["entries"]) >= 20
    assert matrix["unknown_source_policy"] == "reject_unlisted"
    assert matrix["backtest_admission"] == "backtest_safe_only"


def test_missing_backtest_kind_is_rejected(tmp_path: Path) -> None:
    matrix = _matrix()
    for entry in matrix["entries"]:
        entry["backtest_input_kinds"] = [
            kind for kind in entry["backtest_input_kinds"] if kind != "risk_free"
        ]
    with pytest.raises(DataFitnessError, match="backtest input classifications are incomplete"):
        validate_data_fitness_matrix(_write_matrix(tmp_path, matrix), repo_root=REPO_ROOT)


def test_unlisted_evidence_path_is_rejected(tmp_path: Path) -> None:
    matrix = _matrix()
    matrix["entries"][0]["evidence"][0]["path"] = "docs/missing-evidence.md"
    with pytest.raises(DataFitnessError, match="does not identify a file"):
        validate_data_fitness_matrix(_write_matrix(tmp_path, matrix), repo_root=REPO_ROOT)


def test_declared_sources_must_match_entries(tmp_path: Path) -> None:
    matrix = _matrix()
    matrix["source_codes"] = deepcopy(matrix["source_codes"][:-1])
    with pytest.raises(DataFitnessError, match="source_codes does not match entry sources"):
        validate_data_fitness_matrix(_write_matrix(tmp_path, matrix), repo_root=REPO_ROOT)


def test_unknown_matrix_field_is_rejected(tmp_path: Path) -> None:
    matrix = _matrix()
    matrix["entries"][0]["unreviewed"] = True
    with pytest.raises(DataFitnessError, match="contains unknown fields"):
        validate_data_fitness_matrix(_write_matrix(tmp_path, matrix), repo_root=REPO_ROOT)
