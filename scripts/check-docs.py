#!/usr/bin/env python3
"""Validate repository Markdown links, code fences, and JSON examples.

The checker is intentionally dependency-free so it can run locally and in
GitHub-hosted runners without installing a documentation toolchain.
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]
EXCLUDED_PARTS = {
    ".git",
    ".tmp",
    ".visual-brainstorming",
    "bin",
    "dist",
    "local",
    "node_modules",
    "__pycache__",
}
LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
REFERENCE_RE = re.compile(r"^\s*\[[^\]]+\]:\s*(\S+)")
FENCE_RE = re.compile(r"^\s*(```+|~~~+)")


def tracked_files(pattern: str) -> list[Path]:
    return sorted(
        p
        for p in ROOT.rglob(pattern)
        if not any(part in EXCLUDED_PARTS for part in p.relative_to(ROOT).parts)
    )


def normalize_target(raw: str) -> str:
    raw = raw.strip()
    if raw.startswith("<") and raw.endswith(">"):
        raw = raw[1:-1]
    # Markdown titles may follow the URL: (path "title").
    if " " in raw and not raw.startswith(("http://", "https://")):
        raw = raw.split(" ", 1)[0]
    return unquote(raw)


def external_or_anchor(target: str) -> bool:
    lower = target.lower()
    return (
        not target
        or target.startswith("#")
        or lower.startswith(("http://", "https://", "mailto:", "tel:", "data:"))
        or target.startswith("sandbox:")
        or "${{" in target
    )


def validate_markdown(path: Path) -> list[str]:
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")
    active_fence: str | None = None

    for lineno, line in enumerate(text.splitlines(), 1):
        fence = FENCE_RE.match(line)
        if fence:
            token = fence.group(1)[0]
            if active_fence is None:
                active_fence = token
            elif active_fence == token:
                active_fence = None
            continue
        if active_fence is not None:
            continue

        targets = [m.group(1) for m in LINK_RE.finditer(line)]
        ref = REFERENCE_RE.match(line)
        if ref:
            targets.append(ref.group(1))
        for raw in targets:
            target = normalize_target(raw)
            if external_or_anchor(target):
                continue
            file_part = target.split("#", 1)[0].split("?", 1)[0]
            if not file_part:
                continue
            if file_part.startswith("/"):
                resolved = ROOT / file_part.lstrip("/")
            else:
                resolved = path.parent / file_part
            if not resolved.exists():
                rel = path.relative_to(ROOT)
                errors.append(f"{rel}:{lineno}: missing local link target {target!r}")

    if active_fence is not None:
        errors.append(f"{path.relative_to(ROOT)}: unclosed Markdown code fence")
    return errors


def validate_json(path: Path) -> list[str]:
    try:
        json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001 - report exact parser error
        return [f"{path.relative_to(ROOT)}: invalid JSON: {exc}"]
    return []


def main() -> int:
    errors: list[str] = []
    markdown = tracked_files("*.md")
    json_files = tracked_files("*.json")

    for path in markdown:
        errors.extend(validate_markdown(path))
    for path in json_files:
        errors.extend(validate_json(path))

    if errors:
        print("Documentation validation failed:", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
        return 1

    print(f"Validated {len(markdown)} Markdown files and {len(json_files)} JSON files.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
