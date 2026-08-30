"""Fail-closed validation for the checked-in release compatibility contract."""

from __future__ import annotations

import hashlib
import json
import re
import sys
import tomllib
from collections.abc import Mapping
from dataclasses import dataclass
from importlib.metadata import PackageNotFoundError
from importlib.metadata import version as package_version
from pathlib import Path
from typing import Any, Final

MANIFEST_VERSION: Final[str] = "1.0.0"
RELEASE_VERSION: Final[str] = "1.0.0"

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
_SAFE_PATH = re.compile(r"^(?!/)(?!.*(^|/)\.\.(/|$)).+")


class CompatibilityError(ValueError):
    """Raised when a release manifest or repository mix is not supported."""


@dataclass(frozen=True)
class ValidatedRelease:
    """The validated manifest and the repository root it describes."""

    manifest_path: Path
    repo_root: Path
    manifest: dict[str, Any]


def _strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        value = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {constant}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise CompatibilityError(f"invalid release manifest {path}: {error}") from error
    if not isinstance(value, dict):
        raise CompatibilityError(f"release manifest {path} must be a JSON object")
    return value


def _keys(value: Any, expected: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise CompatibilityError(f"{field} must be an object")
    actual = set(value)
    missing = sorted(expected - actual)
    unknown = sorted(actual - expected)
    if missing:
        raise CompatibilityError(f"{field} is missing fields: {', '.join(missing)}")
    if unknown:
        raise CompatibilityError(f"{field} contains unknown fields: {', '.join(unknown)}")
    return value


def _string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise CompatibilityError(f"{field} must be a non-empty string")
    return value


def _semver(value: Any, *, field: str) -> str:
    value = _string(value, field=field)
    if not _SEMVER.fullmatch(value):
        raise CompatibilityError(f"{field} must be semantic version x.y.z")
    return value


def _sha256(value: Any, *, field: str) -> str:
    value = _string(value, field=field)
    if not _SHA256.fullmatch(value):
        raise CompatibilityError(f"{field} must be a lower-case SHA-256")
    return value


def _relative_path(value: Any, *, field: str) -> str:
    value = _string(value, field=field)
    if not _SAFE_PATH.fullmatch(value) or "\x00" in value:
        raise CompatibilityError(f"{field} must be a safe relative path")
    return value.replace("\\", "/")


def _repo_file(repo_root: Path, value: Any, *, field: str) -> Path:
    relative = _relative_path(value, field=field)
    path = (repo_root / relative).resolve()
    try:
        path.relative_to(repo_root)
    except ValueError as error:
        raise CompatibilityError(f"{field} escapes the repository root") from error
    if not path.is_file():
        raise CompatibilityError(f"{field} does not identify a file: {relative}")
    return path


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise CompatibilityError(f"cannot hash {path}: {error}") from error
    return digest.hexdigest()


def _list_of_objects(value: Any, *, field: str) -> list[Mapping[str, Any]]:
    if not isinstance(value, list) or not value:
        raise CompatibilityError(f"{field} must be a non-empty array")
    result: list[Mapping[str, Any]] = []
    for index, item in enumerate(value):
        if not isinstance(item, Mapping):
            raise CompatibilityError(f"{field}[{index}] must be an object")
        result.append(item)
    return result


def _validate_runtime(runtime: Mapping[str, Any], repo_root: Path, *, check_runtime: bool) -> None:
    runtime = _keys(runtime, {"go", "python", "postgres", "grafana", "docker"}, field="runtime")
    go = _keys(runtime["go"], {"module", "version"}, field="runtime.go")
    python = _keys(
        runtime["python"],
        {"version", "package", "package_version", "requires_python", "dependencies", "dev_dependencies"},
        field="runtime.python",
    )
    postgres = _keys(runtime["postgres"], {"major"}, field="runtime.postgres")
    grafana = _keys(runtime["grafana"], {"version"}, field="runtime.grafana")
    docker = _keys(runtime["docker"], {"compose_version"}, field="runtime.docker")

    go_module = _string(go["module"], field="runtime.go.module")
    go_version = _semver(go["version"], field="runtime.go.version")
    python_version = _semver(python["version"], field="runtime.python.version")
    package_name = _string(python["package"], field="runtime.python.package")
    package_release = _semver(python["package_version"], field="runtime.python.package_version")
    requires_python = _string(python["requires_python"], field="runtime.python.requires_python")
    postgres_major = _string(postgres["major"], field="runtime.postgres.major")
    grafana_version = _semver(grafana["version"], field="runtime.grafana.version")
    _semver(docker["compose_version"], field="runtime.docker.compose_version")

    go_mod_path = repo_root / "go.mod"
    try:
        go_mod = go_mod_path.read_text(encoding="utf-8")
    except OSError as error:
        raise CompatibilityError(f"cannot read {go_mod_path}: {error}") from error
    module_match = re.search(r"(?m)^module\s+(\S+)\s*$", go_mod)
    version_match = re.search(r"(?m)^go\s+([0-9]+\.[0-9]+\.[0-9]+)\s*$", go_mod)
    if module_match is None or module_match.group(1) != go_module:
        raise CompatibilityError("runtime.go.module does not match go.mod")
    if version_match is None or version_match.group(1) != go_version:
        raise CompatibilityError("runtime.go.version does not match go.mod")

    pyproject_path = repo_root / "python" / "pyproject.toml"
    try:
        pyproject = tomllib.loads(pyproject_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, tomllib.TOMLDecodeError) as error:
        raise CompatibilityError(f"cannot read {pyproject_path}: {error}") from error
    project = pyproject.get("project")
    if not isinstance(project, Mapping):
        raise CompatibilityError("python/pyproject.toml has no project table")
    if project.get("name") != package_name or project.get("version") != package_release:
        raise CompatibilityError("runtime Python package identity does not match pyproject.toml")
    if project.get("requires-python") != requires_python:
        raise CompatibilityError("runtime.python.requires_python does not match pyproject.toml")

    def dependency_versions(values: Any, *, field: str) -> dict[str, str]:
        if not isinstance(values, list):
            raise CompatibilityError(f"{field} must be an array in pyproject.toml")
        result: dict[str, str] = {}
        for value in values:
            if not isinstance(value, str) or "==" not in value:
                raise CompatibilityError(f"{field} must contain exact == pins")
            name, pinned = value.split("==", 1)
            key = name.lower().replace("_", "-")
            if not key or not _SEMVER.fullmatch(pinned) or key in result:
                raise CompatibilityError(f"{field} contains an invalid or duplicate dependency")
            result[key] = pinned
        return result

    if not isinstance(python["dependencies"], Mapping):
        raise CompatibilityError("runtime.python.dependencies must be an object")
    expected_dependencies = {
        _string(name, field="runtime.python.dependencies name").lower().replace("_", "-"): _semver(
            pinned, field=f"runtime.python.dependencies.{name}"
        )
        for name, pinned in python["dependencies"].items()
    }
    actual_dependencies = dependency_versions(project.get("dependencies"), field="project.dependencies")
    if actual_dependencies != expected_dependencies:
        raise CompatibilityError("runtime Python dependency pins do not match pyproject.toml")

    optional = project.get("optional-dependencies", {})
    if not isinstance(optional, Mapping):
        raise CompatibilityError("pyproject.toml optional dependencies are malformed")
    if not isinstance(python["dev_dependencies"], Mapping):
        raise CompatibilityError("runtime.python.dev_dependencies must be an object")
    expected_dev = {
        name.lower().replace("_", "-"): _semver(value, field=f"runtime.python.dev_dependencies.{name}")
        for name, value in python["dev_dependencies"].items()
    }
    actual_dev = dependency_versions(optional.get("dev"), field="project.optional-dependencies.dev")
    if actual_dev != expected_dev:
        raise CompatibilityError("runtime Python dev dependency pins do not match pyproject.toml")

    if check_runtime:
        running_python = ".".join(str(part) for part in sys.version_info[:3])
        if running_python != python_version:
            raise CompatibilityError(
                f"running Python {running_python} is not the manifest version {python_version}"
            )
        installed = {**expected_dependencies, **expected_dev}
        for name, expected in installed.items():
            try:
                actual = package_version(name)
            except PackageNotFoundError as error:
                raise CompatibilityError(f"required Python package is not installed: {name}") from error
            if actual != expected:
                raise CompatibilityError(f"installed {name} {actual} is not manifest version {expected}")

    if not postgres_major.isdigit() or not grafana_version:
        raise CompatibilityError("runtime service versions are malformed")


def _validate_images(images: Any, repo_root: Path) -> None:
    entries = _list_of_objects(images, field="images")
    expected_services = {
        "collector-build",
        "collector-runtime",
        "jupyter",
        "postgres",
        "grafana",
    }
    seen: set[str] = set()
    for index, item in enumerate(entries):
        item = _keys(item, {"service", "reference", "digest", "source_path"}, field=f"images[{index}]")
        service = _string(item["service"], field=f"images[{index}].service")
        if service in seen:
            raise CompatibilityError(f"images contains duplicate service {service!r}")
        seen.add(service)
        reference = _string(item["reference"], field=f"images[{index}].reference")
        digest = item["digest"]
        if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise CompatibilityError(f"images[{index}].digest must be a sha256 digest")
        source = _repo_file(repo_root, item["source_path"], field=f"images[{index}].source_path")
        try:
            source_text = source.read_text(encoding="utf-8")
        except (OSError, UnicodeError) as error:
            raise CompatibilityError(f"cannot read image source {source}: {error}") from error
        if f"{reference}@{digest}" not in source_text:
            raise CompatibilityError(
                f"images[{index}] does not match pinned image {reference}@{digest} in {source}"
            )
    if seen != expected_services:
        missing = sorted(expected_services - seen)
        extra = sorted(seen - expected_services)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if extra:
            details.append(f"unexpected {', '.join(extra)}")
        raise CompatibilityError("images do not cover the supported services: " + "; ".join(details))


def _validate_file_refs(
    entries: Any,
    repo_root: Path,
    *,
    field: str,
    expected_names: set[str] | None = None,
) -> None:
    values = _list_of_objects(entries, field=field)
    seen: set[str] = set()
    for index, item in enumerate(values):
        item = _keys(item, {"name", "path", "sha256"}, field=f"{field}[{index}]")
        name = _string(item["name"], field=f"{field}[{index}].name")
        if name in seen:
            raise CompatibilityError(f"{field} contains duplicate name {name!r}")
        seen.add(name)
        path = _repo_file(repo_root, item["path"], field=f"{field}[{index}].path")
        expected = _sha256(item["sha256"], field=f"{field}[{index}].sha256")
        actual = _file_sha256(path)
        if actual != expected:
            raise CompatibilityError(f"{field}[{index}] hash mismatch for {item['path']}")
    if expected_names is not None and seen != expected_names:
        missing = sorted(expected_names - seen)
        extra = sorted(seen - expected_names)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if extra:
            details.append(f"unexpected {', '.join(extra)}")
        raise CompatibilityError(f"{field} does not cover the release files: " + "; ".join(details))


def _schema_name(path: str) -> str:
    filename = Path(path).name
    if not filename.endswith(".schema.json"):
        return ""
    return filename[: -len(".schema.json")]


def _validate_schemas(entries: Any, repo_root: Path) -> None:
    values = _list_of_objects(entries, field="contracts.schemas")
    expected_paths = {
        path.relative_to(repo_root).as_posix()
        for path in (repo_root / "schemas").glob("*.schema.json")
        if path.name not in {"common.schema.json", "release-compatibility.schema.json"}
    }
    actual_paths: set[str] = set()
    for index, item in enumerate(values):
        item = _keys(
            item,
            {"name", "path", "schema_version", "sha256"},
            field=f"contracts.schemas[{index}]",
        )
        path_value = _relative_path(item["path"], field=f"contracts.schemas[{index}].path")
        if path_value in actual_paths:
            raise CompatibilityError(f"contracts.schemas contains duplicate path {path_value}")
        actual_paths.add(path_value)
        if path_value not in expected_paths:
            raise CompatibilityError(f"contracts.schemas references unexpected path {path_value}")
        if item["name"] != _schema_name(path_value):
            raise CompatibilityError(f"contracts.schemas[{index}].name does not match its path")
        path = _repo_file(repo_root, path_value, field=f"contracts.schemas[{index}].path")
        expected_hash = _sha256(item["sha256"], field=f"contracts.schemas[{index}].sha256")
        if _file_sha256(path) != expected_hash:
            raise CompatibilityError(f"contracts.schemas hash mismatch for {path_value}")
        document = _strict_json(path)
        if not isinstance(document.get("$id"), str):
            raise CompatibilityError(f"{path_value} has no string $id")
        properties = document.get("properties", {})
        if not isinstance(properties, Mapping):
            raise CompatibilityError(f"{path_value}.properties must be an object")
        schema_property = properties.get("schema_version", {})
        if not isinstance(schema_property, Mapping):
            raise CompatibilityError(f"{path_value}.schema_version must be an object")
        schema_version = schema_property.get("const", "none")
        if item["schema_version"] != schema_version:
            raise CompatibilityError(f"schema version mismatch for {path_value}")
    if actual_paths != expected_paths:
        missing = sorted(expected_paths - actual_paths)
        extra = sorted(actual_paths - expected_paths)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if extra:
            details.append(f"unexpected {', '.join(extra)}")
        raise CompatibilityError("contracts.schemas does not cover the schema catalog: " + "; ".join(details))


def _validate_registries(entries: Any, repo_root: Path) -> None:
    values = _list_of_objects(entries, field="contracts.registries")
    expected = {
        "feature-set": ("schemas/feature-set-registry.json", "registry_version"),
        "feature-taxonomy": ("schemas/feature-taxonomy-registry.json", "registry_version"),
    }
    seen: set[str] = set()
    for index, item in enumerate(values):
        item = _keys(item, {"name", "path", "version", "sha256"}, field=f"contracts.registries[{index}]")
        name = _string(item["name"], field=f"contracts.registries[{index}].name")
        if name in seen:
            raise CompatibilityError(f"contracts.registries contains duplicate name {name!r}")
        seen.add(name)
        if name not in expected:
            raise CompatibilityError(f"contracts.registries contains unsupported registry {name!r}")
        expected_path, version_key = expected[name]
        if item["path"] != expected_path:
            raise CompatibilityError(f"contracts.registries.{name} path is not canonical")
        path = _repo_file(repo_root, item["path"], field=f"contracts.registries[{index}].path")
        if _file_sha256(path) != _sha256(item["sha256"], field=f"contracts.registries[{index}].sha256"):
            raise CompatibilityError(f"contracts.registries hash mismatch for {name}")
        document = _strict_json(path)
        if document.get(version_key) != item["version"]:
            raise CompatibilityError(f"contracts.registries.{name} version does not match its file")
    if seen != set(expected):
        raise CompatibilityError("contracts.registries must include the feature-set and feature-taxonomy registries")


def _validate_migrations(migrations: Mapping[str, Any], repo_root: Path) -> None:
    migrations = _keys(migrations, {"latest", "files"}, field="contracts.migrations")
    values = _list_of_objects(migrations["files"], field="contracts.migrations.files")
    expected_paths = sorted(
        path.relative_to(repo_root).as_posix() for path in (repo_root / "migrations").glob("*.up.sql")
    )
    actual_paths: list[str] = []
    for index, item in enumerate(values):
        item = _keys(item, {"path", "sha256"}, field=f"contracts.migrations.files[{index}]")
        path_value = _relative_path(item["path"], field=f"contracts.migrations.files[{index}].path")
        actual_paths.append(path_value)
        path = _repo_file(repo_root, path_value, field=f"contracts.migrations.files[{index}].path")
        if _file_sha256(path) != _sha256(item["sha256"], field=f"contracts.migrations.files[{index}].sha256"):
            raise CompatibilityError(f"contracts.migrations hash mismatch for {path_value}")
    if actual_paths != expected_paths:
        raise CompatibilityError("contracts.migrations.files must match sorted forward migrations exactly")
    if migrations["latest"] != expected_paths[-1]:
        raise CompatibilityError("contracts.migrations.latest is not the newest forward migration")


def _validate_implementations(entries: Any, repo_root: Path) -> None:
    expected_names = {
        "backtest",
        "paper",
        "market-basic",
        "market-momentum",
        "fundamental-growth",
        "macro-state",
        "bias-audit",
        "strategies",
        "risk",
        "measurement",
        "portfolio",
        "workflow",
    }
    values = _list_of_objects(entries, field="contracts.implementations")
    seen: set[str] = set()
    for index, item in enumerate(values):
        item = _keys(
            item,
            {"name", "path", "version", "sha256"},
            field=f"contracts.implementations[{index}]",
        )
        name = _string(item["name"], field=f"contracts.implementations[{index}].name")
        if name in seen:
            raise CompatibilityError(f"contracts.implementations contains duplicate name {name!r}")
        seen.add(name)
        path = _repo_file(repo_root, item["path"], field=f"contracts.implementations[{index}].path")
        _string(item["version"], field=f"contracts.implementations[{index}].version")
        if _file_sha256(path) != _sha256(item["sha256"], field=f"contracts.implementations[{index}].sha256"):
            raise CompatibilityError(f"contracts.implementations hash mismatch for {name}")
    if seen != expected_names:
        raise CompatibilityError("contracts.implementations does not cover the supported research engines")


def _validate_contracts(contracts: Mapping[str, Any], repo_root: Path) -> None:
    contracts = _keys(
        contracts,
        {"schemas", "registries", "migrations", "implementations", "configuration"},
        field="contracts",
    )
    _validate_schemas(contracts["schemas"], repo_root)
    _validate_registries(contracts["registries"], repo_root)
    _validate_migrations(contracts["migrations"], repo_root)
    _validate_implementations(contracts["implementations"], repo_root)
    _validate_file_refs(
        contracts["configuration"],
        repo_root,
        field="contracts.configuration",
        expected_names={
            "go-module",
            "python-project",
            "compose",
            "collector-dockerfile",
            "jupyter-dockerfile",
            "postgres-dockerfile",
        },
    )


def _validate_security(security: Mapping[str, Any], repo_root: Path) -> None:
    security = _keys(
        security,
        {"bind_address_default", "grafana_anonymous", "credentials_outside_git", "backup_before_upgrade"},
        field="security",
    )
    if security["bind_address_default"] != "127.0.0.1":
        raise CompatibilityError("security.bind_address_default must remain loopback")
    for field in ("grafana_anonymous",):
        if not isinstance(security[field], bool):
            raise CompatibilityError(f"security.{field} must be boolean")
    for field in ("credentials_outside_git", "backup_before_upgrade"):
        if security[field] is not True:
            raise CompatibilityError(f"security.{field} must be true")
    compose = repo_root / "docker-compose.yml"
    try:
        compose_text = compose.read_text(encoding="utf-8")
    except OSError as error:
        raise CompatibilityError(f"cannot read {compose}: {error}") from error
    if compose_text.count("${INVS_BIND_ADDRESS:-127.0.0.1}") < 3:
        raise CompatibilityError("docker-compose.yml must default all host services to loopback")
    if "0.0.0.0:" in compose_text:
        raise CompatibilityError("docker-compose.yml contains an unsafe 0.0.0.0 host binding")
    if 'GF_AUTH_ANONYMOUS_ENABLED: "false"' not in compose_text:
        raise CompatibilityError("Grafana anonymous access must remain disabled")
    env_example = repo_root / ".env.example"
    if "INVS_BIND_ADDRESS=127.0.0.1" not in env_example.read_text(encoding="utf-8"):
        raise CompatibilityError(".env.example must declare the loopback bind default")


def _validate_upgrade(upgrade: Mapping[str, Any]) -> None:
    upgrade = _keys(
        upgrade,
        {"migration_policy", "unsupported_mix_policy", "preflight_command", "backup_command", "restore_command"},
        field="upgrade",
    )
    expected = {
        "migration_policy": "forward_only",
        "unsupported_mix_policy": "reject",
        "preflight_command": "make release-validate",
        "backup_command": "make backup",
        "restore_command": "make restore",
    }
    for field, expected_value in expected.items():
        if upgrade[field] != expected_value:
            raise CompatibilityError(f"upgrade.{field} must be {expected_value!r}")


def _find_repo_root(manifest_path: Path) -> Path:
    for candidate in (manifest_path.parent, *manifest_path.parents):
        if (candidate / "go.mod").is_file() and (candidate / "schemas").is_dir():
            return candidate
    return Path.cwd().resolve()


def validate_release_manifest(
    manifest_path: str | Path = "release/compatibility.json",
    *,
    repo_root: str | Path | None = None,
    check_runtime: bool = False,
) -> ValidatedRelease:
    """Validate the manifest, its fingerprints, and the repository it describes."""

    manifest_file = Path(manifest_path).resolve()
    root = Path(repo_root).resolve() if repo_root is not None else _find_repo_root(manifest_file)
    manifest = _strict_json(manifest_file)
    _keys(
        manifest,
        {"$schema", "manifest_version", "release_version", "runtime", "images", "contracts", "security", "upgrade"},
        field="manifest",
    )
    if manifest["$schema"] != "../schemas/release-compatibility.schema.json":
        raise CompatibilityError("manifest.$schema is not the supported release schema")
    if manifest["manifest_version"] != MANIFEST_VERSION:
        raise CompatibilityError(f"unsupported manifest version {manifest['manifest_version']!r}")
    if manifest["release_version"] != RELEASE_VERSION:
        raise CompatibilityError(f"unsupported release version {manifest['release_version']!r}")
    _validate_runtime(manifest["runtime"], root, check_runtime=check_runtime)
    _validate_images(manifest["images"], root)
    _validate_contracts(manifest["contracts"], root)
    _validate_security(manifest["security"], root)
    _validate_upgrade(manifest["upgrade"])
    return ValidatedRelease(manifest_path=manifest_file, repo_root=root, manifest=manifest)


__all__ = [
    "MANIFEST_VERSION",
    "RELEASE_VERSION",
    "CompatibilityError",
    "ValidatedRelease",
    "validate_release_manifest",
]
