from __future__ import annotations

import json
from pathlib import Path

from research.workflow_cli import main


def test_workflow_cli_reports_invalid_missing_spec(tmp_path: Path, capsys) -> None:
    exit_code = main(["build", "--spec", str(tmp_path / "missing.json"), "--output", str(tmp_path / "report.json")])

    assert exit_code == 1
    assert "invs-workflow:" in capsys.readouterr().err


def test_workflow_cli_validate_reports_shape_error(tmp_path: Path, capsys) -> None:
    report = tmp_path / "report.json"
    report.write_text(json.dumps({"schema_version": "wrong"}), encoding="utf-8")

    exit_code = main(["validate", "--report", str(report), "--repo-root", str(tmp_path)])

    assert exit_code == 1
    assert "invs-workflow:" in capsys.readouterr().err
