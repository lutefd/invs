#!/usr/bin/env python3
"""Dependency-free integrity checks for the local JSON Schema catalog."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from urllib.parse import urldefrag


ROOT = Path(__file__).resolve().parent


def resolve_pointer(document: object, fragment: str) -> object:
    if not fragment:
        return document
    if not fragment.startswith("/"):
        raise ValueError(f"unsupported non-pointer fragment #{fragment}")
    current = document
    for raw_token in fragment[1:].split("/"):
        token = raw_token.replace("~1", "/").replace("~0", "~")
        if not isinstance(current, dict) or token not in current:
            raise KeyError(f"missing JSON Pointer token {token!r}")
        current = current[token]
    return current


def walk(node: object):
    if isinstance(node, dict):
        yield node
        for value in node.values():
            yield from walk(value)
    elif isinstance(node, list):
        for value in node:
            yield from walk(value)


def main() -> int:
    paths = sorted(ROOT.glob("*.schema.json"))
    documents = {path.name: json.loads(path.read_text()) for path in paths}
    errors: list[str] = []
    seen_ids: dict[str, str] = {}

    for name, document in documents.items():
        if document.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            errors.append(f"{name}: expected JSON Schema 2020-12")
        schema_id = document.get("$id")
        if not isinstance(schema_id, str):
            errors.append(f"{name}: missing string $id")
        elif schema_id in seen_ids:
            errors.append(f"{name}: duplicate $id also used by {seen_ids[schema_id]}")
        else:
            seen_ids[schema_id] = name

        if name != "common.schema.json":
            properties = document.get("properties", {})
            undeclared = set(document.get("required", [])) - set(properties)
            if undeclared:
                errors.append(f"{name}: required properties are undeclared: {sorted(undeclared)}")
            if document.get("additionalProperties") is not False:
                errors.append(f"{name}: canonical entity must reject unknown properties")

        for node in walk(document):
            ref = node.get("$ref")
            if not isinstance(ref, str):
                continue
            target, fragment = urldefrag(ref)
            target_name = target or name
            if "://" in target_name:
                errors.append(f"{name}: remote $ref is not allowed: {ref}")
                continue
            target_document = documents.get(target_name)
            if target_document is None:
                errors.append(f"{name}: missing referenced schema {target_name!r}")
                continue
            try:
                resolve_pointer(target_document, fragment)
            except (KeyError, ValueError) as exc:
                errors.append(f"{name}: invalid $ref {ref!r}: {exc}")

    if errors:
        print("schema catalog validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(f"validated {len(documents)} JSON Schema documents")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
