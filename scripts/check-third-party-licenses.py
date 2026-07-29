#!/usr/bin/env python3
"""Fail when runtime dependencies and the shipped license inventory diverge."""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
from pathlib import Path, PurePosixPath


ROOT = Path(__file__).resolve().parent.parent
MANIFEST_PATH = ROOT / "LICENSES/runtime-dependencies.json"
ENTRY_FIELDS = {"version", "license", "license_files"}
LICENSE_IDS = {"Apache-2.0", "BSD-3-Clause", "BSD-3-Clause AND MIT", "MIT"}
PROJECT_LICENSE_FILES = {
    "LICENSES/MIT-SuperFeishuSearch-materials.txt",
    "LICENSES/runtime-dependencies.json",
}
RELEASE_TARGETS = (
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
)


def fail(message: str) -> None:
    raise SystemExit(f"third-party license check failed: {message}")


def load_manifest() -> dict[str, object]:
    try:
        manifest = json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot read {MANIFEST_PATH.relative_to(ROOT)}: {error}")
    if not isinstance(manifest, dict) or set(manifest) != {
        "version",
        "license_files",
        "go_modules",
        "npm_packages",
    }:
        fail("manifest must contain exactly version, license_files, go_modules, npm_packages")
    if manifest["version"] != 1:
        fail(f"unsupported manifest version: {manifest['version']!r}")
    return manifest


def validate_license_files(raw: object) -> set[str]:
    if not isinstance(raw, dict) or not raw:
        fail("license_files must be a non-empty object")
    known: set[str] = set()
    for name, expected_hash in raw.items():
        if not isinstance(name, str) or not isinstance(expected_hash, str):
            fail("license_files keys and hashes must be strings")
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts or not name.startswith("LICENSES/"):
            fail(f"unsafe license path: {name!r}")
        if len(expected_hash) != 64 or any(char not in "0123456789abcdef" for char in expected_hash):
            fail(f"invalid SHA-256 for {name}")
        local = ROOT.joinpath(*path.parts)
        if not local.is_file() or local.is_symlink():
            fail(f"missing or unsafe license file: {name}")
        actual_hash = hashlib.sha256(local.read_bytes()).hexdigest()
        if actual_hash != expected_hash:
            fail(f"license hash mismatch for {name}: {actual_hash} != {expected_hash}")
        known.add(name)
    return known


def validate_entries(label: str, raw: object, known_files: set[str]) -> tuple[dict[str, str], set[str]]:
    if not isinstance(raw, dict) or not raw:
        fail(f"{label} must be a non-empty object")
    versions: dict[str, str] = {}
    used_files: set[str] = set()
    for name, entry in raw.items():
        if not isinstance(name, str) or not isinstance(entry, dict) or set(entry) != ENTRY_FIELDS:
            fail(f"invalid {label} entry for {name!r}")
        version = entry["version"]
        license_id = entry["license"]
        files = entry["license_files"]
        if not isinstance(version, str) or not version or license_id not in LICENSE_IDS:
            fail(f"invalid version or license identifier for {label} {name}")
        if not isinstance(files, list) or not files or any(not isinstance(item, str) for item in files):
            fail(f"{label} {name} must reference at least one license file")
        unknown = set(files) - known_files
        if unknown:
            fail(f"{label} {name} references unknown license files: {sorted(unknown)}")
        used_files.update(files)
        versions[name] = version
    return versions, used_files


def tracked_license_files() -> set[str]:
    result = subprocess.run(
        ["git", "ls-files", "-z", "--", "LICENSES"],
        cwd=ROOT,
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        fail(f"git ls-files failed: {result.stderr.decode(errors='replace').strip()}")
    return {item.decode("utf-8") for item in result.stdout.split(b"\0") if item}


def actual_go_modules() -> dict[str, str]:
    template = "{{with .Module}}{{if ne .Main true}}{{.Path}}\t{{.Version}}{{end}}{{end}}"
    modules: dict[str, str] = {}
    for goos, goarch in RELEASE_TARGETS:
        environment = os.environ.copy()
        environment.update({"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"})
        result = subprocess.run(
            ["go", "list", "-deps", "-f", template, "./cmd/sfs"],
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            fail(f"go list failed for {goos}/{goarch}: {result.stderr.strip()}")
        for line in result.stdout.splitlines():
            if not line.strip():
                continue
            try:
                name, version = line.split("\t", 1)
            except ValueError:
                fail(f"unexpected go list output for {goos}/{goarch}: {line!r}")
            previous = modules.setdefault(name, version)
            if previous != version:
                fail(f"multiple versions of Go module {name}: {previous}, {version}")
    return modules


def npm_package_name(location: str) -> str:
    marker = "/node_modules/"
    if marker in location:
        return location.rsplit(marker, 1)[1]
    return location.removeprefix("node_modules/")


def actual_npm_packages() -> dict[str, str]:
    lock_path = ROOT / "web/package-lock.json"
    try:
        lock = json.loads(lock_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot read web/package-lock.json: {error}")
    if lock.get("lockfileVersion") != 3 or not isinstance(lock.get("packages"), dict):
        fail("web/package-lock.json must use lockfileVersion 3")
    packages: dict[str, str] = {}
    for location, entry in lock["packages"].items():
        if not location or not location.startswith("node_modules/") or entry.get("dev", False):
            continue
        name = npm_package_name(location)
        version = entry.get("version")
        if not isinstance(version, str) or not version:
            fail(f"production npm package {location} has no version")
        previous = packages.setdefault(name, version)
        if previous != version:
            fail(f"multiple production versions of npm package {name}: {previous}, {version}")
    return packages


def compare(label: str, expected: dict[str, str], actual: dict[str, str]) -> None:
    if expected == actual:
        return
    missing = sorted(set(expected) - set(actual))
    unlicensed = sorted(set(actual) - set(expected))
    drift = sorted(
        f"{name}: expected {expected[name]}, got {actual[name]}"
        for name in set(expected) & set(actual)
        if expected[name] != actual[name]
    )
    fail(f"{label} inventory drift; missing={missing}, unlicensed={unlicensed}, versions={drift}")


def main() -> int:
    manifest = load_manifest()
    known_files = validate_license_files(manifest["license_files"])
    expected_go, go_files = validate_entries("go_modules", manifest["go_modules"], known_files)
    expected_npm, npm_files = validate_entries("npm_packages", manifest["npm_packages"], known_files)
    if go_files | npm_files != known_files:
        fail(f"unreferenced runtime license files: {sorted(known_files - go_files - npm_files)}")
    expected_tracked = known_files | PROJECT_LICENSE_FILES
    actual_tracked = tracked_license_files()
    if actual_tracked != expected_tracked:
        fail(
            "tracked LICENSES inventory drift; "
            f"missing={sorted(expected_tracked - actual_tracked)}, extra={sorted(actual_tracked - expected_tracked)}"
        )
    compare("Go runtime modules", expected_go, actual_go_modules())
    compare("Web production packages", expected_npm, actual_npm_packages())
    print(
        f"Validated {len(expected_go)} Go modules, {len(expected_npm)} Web production packages, "
        f"and {len(known_files)} runtime license files."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
