#!/usr/bin/env python3
"""Validate the complete GitHub Release bundle before publication."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import tarfile
import tempfile
import zipfile
from dataclasses import dataclass
from pathlib import Path, PurePosixPath


TARGETS = (
    ("linux", "amd64", ".tar.gz"),
    ("linux", "arm64", ".tar.gz"),
    ("darwin", "amd64", ".tar.gz"),
    ("darwin", "arm64", ".tar.gz"),
    ("windows", "amd64", ".zip"),
    ("windows", "arm64", ".zip"),
)
RUNTIME_REQUIRED = {
    "CHANGELOG.md",
    "INSTALL.txt",
    "LICENSE",
    "README.md",
    "THIRD_PARTY_NOTICES.md",
    "config.demo.json",
    "config.example.json",
    "docs/CLI_REFERENCE.md",
    "docs/CONFIGURATION.md",
    "docs/GETTING_STARTED.md",
    "docs/TROUBLESHOOTING.md",
    "skills/super-feishu-search/SKILL.md",
}
FORBIDDEN_SOURCE_PREFIXES = (
    ".git/",
    ".tmp/",
    ".visual-brainstorming/",
    "bin/",
    "dist/",
    "local/",
    "minutes/",
    "web/dist/",
    "web/node_modules/",
)
FORBIDDEN_SOURCE_NAMES = {".ds_store", ".env", "id_rsa", "id_ed25519"}


class VerificationError(RuntimeError):
    pass


@dataclass(frozen=True)
class ArchiveEntry:
    name: str
    data: bytes | None
    is_dir: bool
    is_link: bool
    mode: int


def fail(message: str) -> None:
    raise VerificationError(message)


def repository_license_hashes(repository: Path) -> dict[str, str]:
    result = subprocess.run(
        ["git", "-C", str(repository), "ls-tree", "-rz", "HEAD", "--", "LICENSES"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        fail(f"cannot enumerate tracked LICENSES: {result.stderr.decode(errors='replace').strip()}")
    hashes: dict[str, str] = {}
    for record in result.stdout.split(b"\0"):
        if not record:
            continue
        try:
            metadata, raw_name = record.split(b"\t", 1)
            mode, object_type, object_id = metadata.decode("ascii").split(" ", 2)
            name = raw_name.decode("utf-8")
        except (UnicodeDecodeError, ValueError) as error:
            fail(f"invalid git ls-tree LICENSES record: {record!r}: {error}")
        path = PurePosixPath(name)
        if object_type != "blob" or mode != "100644" or path.is_absolute() or ".." in path.parts:
            fail(f"tracked license is not a regular 0644 file: {name}")
        blob = subprocess.run(
            ["git", "-C", str(repository), "cat-file", "blob", object_id],
            check=False,
            capture_output=True,
        )
        if blob.returncode != 0:
            fail(f"cannot read tracked license {name}: {blob.stderr.decode(errors='replace').strip()}")
        hashes[name] = hashlib.sha256(blob.stdout).hexdigest()
    if "LICENSES/runtime-dependencies.json" not in hashes:
        fail("repository is missing LICENSES/runtime-dependencies.json")
    return hashes


def validate_release_directory(path: Path) -> tuple[Path, list[Path]]:
    directory = Path(os.path.abspath(path.expanduser()))
    if directory.is_symlink() or not directory.is_dir():
        fail("release directory is missing or is a symlink")
    entries = list(directory.iterdir())
    if any(entry.is_symlink() or not entry.is_file() for entry in entries):
        fail("release directory contains a symlink, directory, or non-regular entry")
    return directory, entries


def safe_name(name: str) -> str:
    normalized = name.rstrip("/")
    path = PurePosixPath(normalized)
    if not normalized or path.is_absolute() or ".." in path.parts or "\\" in normalized:
        fail(f"unsafe archive entry: {name!r}")
    if path.as_posix() != normalized:
        fail(f"non-canonical archive entry: {name!r}")
    return normalized


def read_archive(path: Path) -> list[ArchiveEntry]:
    entries: list[ArchiveEntry] = []
    if path.name.endswith(".tar.gz"):
        with tarfile.open(path, "r:gz") as archive:
            for member in archive.getmembers():
                name = safe_name(member.name)
                is_link = member.issym() or member.islnk()
                data = None
                if member.isfile():
                    stream = archive.extractfile(member)
                    if stream is None:
                        fail(f"cannot read {member.name} from {path.name}")
                    data = stream.read()
                elif not member.isdir() and not is_link:
                    fail(f"unsupported tar entry type: {member.name}")
                entries.append(ArchiveEntry(name, data, member.isdir(), is_link, member.mode & 0o777))
    elif path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            for info in archive.infolist():
                name = safe_name(info.filename)
                mode = (info.external_attr >> 16) & 0o777
                is_dir = info.is_dir()
                file_type = (info.external_attr >> 16) & 0o170000
                is_link = file_type == 0o120000
                entries.append(ArchiveEntry(name, None if is_dir else archive.read(info), is_dir, is_link, mode))
    else:
        fail(f"unsupported archive format: {path.name}")
    names = [entry.name for entry in entries]
    if len(names) != len(set(names)):
        fail(f"duplicate archive entries in {path.name}")
    if any(entry.is_link for entry in entries):
        fail(f"links are not allowed in release archive {path.name}")
    return entries


def files_under_root(entries: list[ArchiveEntry], expected_root: str) -> dict[str, ArchiveEntry]:
    roots = {PurePosixPath(entry.name).parts[0] for entry in entries}
    if roots != {expected_root}:
        fail(f"archive root mismatch: got {sorted(roots)}, want {expected_root}")
    prefix = expected_root + "/"
    return {
        entry.name[len(prefix) :]: entry
        for entry in entries
        if not entry.is_dir and entry.name.startswith(prefix)
    }


def verify_checksums(directory: Path, expected_archives: set[str]) -> None:
    checksum_path = directory / "SHA256SUMS"
    lines = [line.strip() for line in checksum_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    parsed: dict[str, str] = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})\s+\*?([^/\\]+)", line)
        if not match:
            fail(f"invalid SHA256SUMS line: {line!r}")
        digest, name = match.groups()
        if name in parsed:
            fail(f"duplicate checksum entry: {name}")
        parsed[name] = digest
    if set(parsed) != expected_archives:
        fail(f"checksum file set mismatch: got {sorted(parsed)}, want {sorted(expected_archives)}")
    for name, expected in parsed.items():
        actual = hashlib.sha256((directory / name).read_bytes()).hexdigest()
        if actual != expected:
            fail(f"checksum mismatch for {name}: {actual} != {expected}")


def go_build_settings(binary: Path) -> dict[str, str]:
    result = subprocess.run(
        ["go", "version", "-m", str(binary)],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        fail(f"go version -m failed for {binary.name}: {result.stderr.strip()}")
    settings: dict[str, str] = {}
    for raw in result.stdout.splitlines():
        line = raw.strip()
        if not line.startswith("build\t"):
            continue
        value = line.removeprefix("build\t")
        if "=" in value:
            key, item = value.split("=", 1)
            settings[key] = item
        else:
            settings[value] = "true"
    return settings


def verify_runtime_archive(
    directory: Path,
    version: str,
    commit: str,
    license_hashes: dict[str, str],
    goos: str,
    goarch: str,
    suffix: str,
    execute_runtime: bool,
) -> None:
    root = f"SuperFeishuSearch-{version}-{goos}-{goarch}"
    archive_path = directory / f"{root}{suffix}"
    files = files_under_root(read_archive(archive_path), root)
    missing = (RUNTIME_REQUIRED | set(license_hashes)) - set(files)
    if missing:
        fail(f"{archive_path.name} is missing runtime files: {sorted(missing)}")
    for name, expected_hash in license_hashes.items():
        actual_hash = hashlib.sha256(files[name].data or b"").hexdigest()
        if actual_hash != expected_hash:
            fail(f"{archive_path.name} has modified license file {name}")
    archived_licenses = {name for name in files if name.startswith("LICENSES/")}
    if archived_licenses != set(license_hashes):
        fail(
            f"{archive_path.name} has unexpected license files: "
            f"{sorted(archived_licenses - set(license_hashes))}"
        )
    binary_name = "sfs.exe" if goos == "windows" else "sfs"
    binary_entry = files.get(binary_name)
    if binary_entry is None or binary_entry.data is None:
        fail(f"{archive_path.name} is missing {binary_name}")
    if goos != "windows" and binary_entry.mode & 0o111 == 0:
        fail(f"{archive_path.name} binary is not executable")
    demo = json.loads(files["config.demo.json"].data or b"{}")
    if demo.get("backend") != "mock" or demo.get("ai", {}).get("provider") != "demo":
        fail(f"{archive_path.name} has an invalid offline demo configuration")
    with tempfile.TemporaryDirectory(prefix="sfs-release-") as temporary:
        binary = Path(temporary) / binary_name
        binary.write_bytes(binary_entry.data)
        binary.chmod(0o700)
        settings = go_build_settings(binary)
        expected = {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0", "-trimpath": "true"}
        for key, value in expected.items():
            if settings.get(key) != value:
                fail(f"{archive_path.name} build setting {key}={settings.get(key)!r}, want {value!r}")
        raw = binary_entry.data
        if version.encode() not in raw or commit.encode() not in raw:
            fail(f"{archive_path.name} does not embed version={version} and commit={commit}")
        if execute_runtime and goos == "linux" and goarch == "amd64":
            try:
                version_result = subprocess.run(
                    [str(binary), "version"],
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
            except (OSError, subprocess.TimeoutExpired) as error:
                fail(f"Linux amd64 version smoke could not complete: {error}")
            expected_version = f"sfs {version} commit={commit} "
            if version_result.returncode != 0 or expected_version not in version_result.stdout:
                fail(f"Linux amd64 version smoke failed: {version_result.stdout.strip()} {version_result.stderr.strip()}")
            config_path = Path(temporary) / "config.demo.json"
            config_path.write_bytes(files["config.demo.json"].data or b"")
            try:
                smoke = subprocess.run(
                    [str(binary), "--config", str(config_path), "--output", "json", "search", "A 项目 延期", "--sources", "docs,messages"],
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
            except (OSError, subprocess.TimeoutExpired) as error:
                fail(f"Linux amd64 offline demo smoke could not complete: {error}")
            if smoke.returncode != 0:
                fail(f"Linux amd64 offline demo smoke failed: {smoke.stderr.strip()}")
            payload = json.loads(smoke.stdout)
            if not payload.get("session_id") or not payload.get("candidates"):
                fail("Linux amd64 offline demo smoke returned no session or candidates")


def source_inventory(entries: list[ArchiveEntry], root: str) -> dict[str, tuple[str, int]]:
    files = files_under_root(entries, root)
    return {
        name: (hashlib.sha256(entry.data or b"").hexdigest(), entry.mode & 0o777)
        for name, entry in files.items()
    }


def repository_source_inventory(repository: Path) -> dict[str, tuple[str, int]]:
    result = subprocess.run(
        ["git", "-C", str(repository), "ls-tree", "-rz", "--full-tree", "HEAD"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        fail(f"cannot enumerate tracked source tree: {result.stderr.decode(errors='replace').strip()}")
    inventory: dict[str, tuple[str, int]] = {}
    for record in result.stdout.split(b"\0"):
        if not record:
            continue
        try:
            metadata, raw_name = record.split(b"\t", 1)
            mode, object_type, object_id = metadata.decode("ascii").split(" ", 2)
            name = raw_name.decode("utf-8")
        except (UnicodeDecodeError, ValueError) as error:
            fail(f"invalid git source tree record: {record!r}: {error}")
        path = PurePosixPath(name)
        if (
            object_type != "blob"
            or mode not in {"100644", "100755"}
            or path.is_absolute()
            or ".." in path.parts
            or path.as_posix() != name
        ):
            fail(f"tracked source entry is not a canonical regular file: {name}")
        blob = subprocess.run(
            ["git", "-C", str(repository), "cat-file", "blob", object_id],
            check=False,
            capture_output=True,
        )
        if blob.returncode != 0:
            fail(f"cannot read tracked source {name}: {blob.stderr.decode(errors='replace').strip()}")
        inventory[name] = (hashlib.sha256(blob.stdout).hexdigest(), int(mode[-3:], 8))
    if not inventory:
        fail("tracked source tree is empty")
    return inventory


def materialize_source_archive(entries: list[ArchiveEntry], root: str, destination: Path) -> None:
    if any(entry.is_link for entry in entries):
        fail("links are not allowed while materializing a source archive")
    files = files_under_root(entries, root)
    try:
        destination.mkdir(parents=True, exist_ok=False)
        for name, entry in sorted(files.items()):
            if entry.data is None:
                fail(f"source archive entry has no data: {name}")
            target = destination.joinpath(*PurePosixPath(name).parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(entry.data)
            target.chmod(entry.mode or 0o644)
    except OSError as error:
        fail(f"cannot safely materialize source archive: {error}")


def verify_materialized_source(entries: list[ArchiveEntry], root: str) -> None:
    with tempfile.TemporaryDirectory(prefix="sfs-source-verify-") as temporary:
        source = Path(temporary) / root
        materialize_source_archive(entries, root, source)
        try:
            result = subprocess.run(
                ["make", "verify"],
                cwd=source,
                check=False,
                capture_output=True,
                text=True,
                timeout=12 * 60,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            fail(f"source archive make verify could not complete: {error}")
        if result.returncode != 0:
            output = (result.stdout + "\n" + result.stderr).strip()
            fail(f"source archive make verify failed:\n{output[-12000:]}")


def verify_source_archives(
    directory: Path,
    version: str,
    repository: Path,
    execute_source: bool,
) -> None:
    root = f"SuperFeishuSearch-{version}-source"
    tar_entries = read_archive(directory / f"{root}.tar.gz")
    zip_entries = read_archive(directory / f"{root}.zip")
    tar_inventory = source_inventory(tar_entries, root)
    zip_inventory = source_inventory(zip_entries, root)
    if tar_inventory != zip_inventory:
        fail("source ZIP and TAR.GZ contents or file modes differ")
    tracked_inventory = repository_source_inventory(repository)
    if tar_inventory != tracked_inventory:
        missing = sorted(set(tracked_inventory) - set(tar_inventory))
        extra = sorted(set(tar_inventory) - set(tracked_inventory))
        modified = sorted(
            name
            for name in set(tar_inventory) & set(tracked_inventory)
            if tar_inventory[name] != tracked_inventory[name]
        )
        fail(
            "source archive differs from tracked Git tree; "
            f"missing={missing[:20]} extra={extra[:20]} modified={modified[:20]}"
        )
    for name in tar_inventory:
        lowered = name.lower()
        base = PurePosixPath(name).name
        if any(lowered.startswith(prefix.lower()) for prefix in FORBIDDEN_SOURCE_PREFIXES):
            fail(f"forbidden source path included: {name}")
        if base.lower() in FORBIDDEN_SOURCE_NAMES or base.lower().startswith(".env"):
            fail(f"forbidden sensitive filename included: {name}")
    for required in (
        "LICENSE",
        "LICENSES/MIT-SuperFeishuSearch-materials.txt",
        "LICENSES/MIT-larksuite-oapi-sdk-go.txt",
        "LICENSES/runtime-dependencies.json",
        "THIRD_PARTY_NOTICES.md",
        "config.demo.json",
    ):
        if required not in tar_inventory:
            fail(f"source archives are missing {required}")
    if execute_source:
        verify_materialized_source(zip_entries, root)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument(
        "--archive-execution",
        choices=("required", "source", "skip"),
        default="required",
        help="execute Linux demo and source verify, source verify only, or passive archive verification only",
    )
    args = parser.parse_args()
    directory, directory_entries = validate_release_directory(args.dir)
    version = args.version.removeprefix("v")
    commit = args.commit.strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        fail(f"invalid stable release version: {version}")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        fail(f"release commit must be a full lowercase Git SHA: {commit}")
    archive_names = {
        f"SuperFeishuSearch-{version}-{goos}-{goarch}{suffix}"
        for goos, goarch, suffix in TARGETS
    }
    archive_names.update(
        {
            f"SuperFeishuSearch-{version}-source.tar.gz",
            f"SuperFeishuSearch-{version}-source.zip",
        }
    )
    expected_files = archive_names | {"SHA256SUMS"}
    actual_files = {path.name for path in directory_entries}
    if actual_files != expected_files:
        fail(f"release must contain exactly 9 files; got={sorted(actual_files)} want={sorted(expected_files)}")
    repository = Path(__file__).resolve().parent.parent
    license_hashes = repository_license_hashes(repository)
    verify_checksums(directory, archive_names)
    execute_runtime = args.archive_execution == "required"
    execute_source = args.archive_execution != "skip"
    for target in TARGETS:
        verify_runtime_archive(
            directory,
            version,
            commit,
            license_hashes,
            *target,
            execute_runtime=execute_runtime,
        )
    verify_source_archives(directory, version, repository, execute_source)
    print(f"Verified 9 release files for SuperFeishuSearch {version} at {commit}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except VerificationError as error:
        raise SystemExit(f"release verification failed: {error}") from error
