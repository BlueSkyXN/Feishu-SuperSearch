from __future__ import annotations

import hashlib
import importlib.util
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("verify-release.py")
SPEC = importlib.util.spec_from_file_location("verify_release", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
VERIFY_RELEASE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = VERIFY_RELEASE
SPEC.loader.exec_module(VERIFY_RELEASE)


class ReleaseDirectoryTest(unittest.TestCase):
    def test_accepts_regular_files(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "asset.txt").write_text("asset", encoding="utf-8")
            directory, entries = VERIFY_RELEASE.validate_release_directory(root)
            self.assertEqual(directory, root)
            self.assertEqual([entry.name for entry in entries], ["asset.txt"])

    def test_rejects_release_directory_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            real = root / "real"
            real.mkdir()
            link = root / "release"
            link.symlink_to(real, target_is_directory=True)
            with self.assertRaises(VERIFY_RELEASE.VerificationError):
                VERIFY_RELEASE.validate_release_directory(link)

    def test_rejects_child_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "target"
            target.write_text("asset", encoding="utf-8")
            release = root / "release"
            release.mkdir()
            (release / "asset.txt").symlink_to(target)
            with self.assertRaises(VERIFY_RELEASE.VerificationError):
                VERIFY_RELEASE.validate_release_directory(release)


class SourceArchiveMaterializationTest(unittest.TestCase):
    def entry(self, name: str, data: bytes, mode: int = 0o644) -> object:
        return VERIFY_RELEASE.ArchiveEntry(name, data, False, False, mode)

    def runtime_entries(self, version: str, commit: str) -> tuple[list[object], dict[str, str]]:
        root = f"SuperFeishuSearch-{version}-linux-amd64"
        contents = {name: name.encode() for name in VERIFY_RELEASE.RUNTIME_REQUIRED}
        contents["config.demo.json"] = b'{"backend":"mock","ai":{"provider":"demo"}}'
        contents["sfs"] = f"binary {version} {commit}".encode()
        contents["LICENSES/runtime/test.txt"] = b"license"
        entries = [
            self.entry(f"{root}/{name}", data, 0o755 if name == "sfs" else 0o644)
            for name, data in sorted(contents.items())
        ]
        return entries, {"LICENSES/runtime/test.txt": hashlib.sha256(b"license").hexdigest()}

    def test_rejects_non_canonical_archive_name(self) -> None:
        with self.assertRaises(VERIFY_RELEASE.VerificationError):
            VERIFY_RELEASE.safe_name("source/./README.md")

    def test_materializes_files_and_preserves_executable_mode(self) -> None:
        entries = [
            self.entry("source/README.md", b"source"),
            self.entry("source/scripts/check.sh", b"#!/bin/sh\nexit 0\n", 0o755),
        ]
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / "source"
            VERIFY_RELEASE.materialize_source_archive(entries, "source", destination)
            self.assertEqual((destination / "README.md").read_bytes(), b"source")
            self.assertTrue((destination / "scripts/check.sh").stat().st_mode & 0o111)

    def test_rejects_link_during_materialization(self) -> None:
        entries = [VERIFY_RELEASE.ArchiveEntry("source/link", b"target", False, True, 0o777)]
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(VERIFY_RELEASE.VerificationError):
                VERIFY_RELEASE.materialize_source_archive(entries, "source", Path(temporary) / "source")

    def test_runs_make_verify_in_materialized_source(self) -> None:
        entries = [
            self.entry("source/Makefile", b"verify:\n\t@test -x scripts/check.sh\n\t@./scripts/check.sh\n"),
            self.entry("source/scripts/check.sh", b"#!/bin/sh\nset -eu\ntest -f README.md\n", 0o755),
            self.entry("source/README.md", b"source"),
        ]
        VERIFY_RELEASE.verify_materialized_source(entries, "source")

    def test_passive_source_verification_does_not_execute_archive(self) -> None:
        version = "9.8.7"
        root = f"SuperFeishuSearch-{version}-source"
        required = {
            "LICENSE",
            "LICENSES/MIT-SuperFeishuSearch-materials.txt",
            "LICENSES/MIT-larksuite-oapi-sdk-go.txt",
            "LICENSES/runtime-dependencies.json",
            "THIRD_PARTY_NOTICES.md",
            "config.demo.json",
        }
        entries = [self.entry(f"{root}/{name}", name.encode()) for name in sorted(required)]
        tracked = VERIFY_RELEASE.source_inventory(entries, root)

        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            with (
                mock.patch.object(VERIFY_RELEASE, "read_archive", side_effect=[entries, entries]),
                mock.patch.object(
                    VERIFY_RELEASE,
                    "repository_source_inventory",
                    return_value=tracked,
                ),
                mock.patch.object(VERIFY_RELEASE, "verify_materialized_source") as execute,
            ):
                VERIFY_RELEASE.verify_source_archives(directory, version, directory, False)

            execute.assert_not_called()

    def test_passive_runtime_verification_does_not_execute_binary(self) -> None:
        version = "9.8.7"
        commit = "a" * 40
        entries, licenses = self.runtime_entries(version, commit)
        settings = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "-trimpath": "true"}

        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(VERIFY_RELEASE, "read_archive", return_value=entries),
                mock.patch.object(VERIFY_RELEASE, "go_build_settings", return_value=settings),
                mock.patch.object(
                    VERIFY_RELEASE.subprocess,
                    "run",
                    side_effect=AssertionError("passive verification must not execute the binary"),
                ),
            ):
                VERIFY_RELEASE.verify_runtime_archive(
                    Path(temporary),
                    version,
                    commit,
                    licenses,
                    "linux",
                    "amd64",
                    ".tar.gz",
                    False,
                )

    def test_runtime_timeout_is_reported_as_verification_error(self) -> None:
        version = "9.8.7"
        commit = "b" * 40
        entries, licenses = self.runtime_entries(version, commit)
        settings = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "-trimpath": "true"}

        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(VERIFY_RELEASE, "read_archive", return_value=entries),
                mock.patch.object(VERIFY_RELEASE, "go_build_settings", return_value=settings),
                mock.patch.object(
                    VERIFY_RELEASE.subprocess,
                    "run",
                    side_effect=VERIFY_RELEASE.subprocess.TimeoutExpired(["sfs", "version"], 30),
                ),
            ):
                with self.assertRaisesRegex(VERIFY_RELEASE.VerificationError, "version smoke could not complete"):
                    VERIFY_RELEASE.verify_runtime_archive(
                        Path(temporary),
                        version,
                        commit,
                        licenses,
                        "linux",
                        "amd64",
                        ".tar.gz",
                        True,
                    )

    @unittest.skipUnless(shutil.which("git"), "git is required for HEAD inventory verification")
    def test_repository_source_inventory_uses_head_blob_and_mode(self) -> None:
        repository = SCRIPT.parent.parent.resolve()
        root = VERIFY_RELEASE.subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=repository,
            check=False,
            capture_output=True,
            text=True,
        )
        if root.returncode != 0 or Path(root.stdout.strip()).resolve() != repository:
            self.skipTest("test requires an exact Git checkout root")

        blob = VERIFY_RELEASE.subprocess.run(
            ["git", "show", "HEAD:scripts/check-version.sh"],
            cwd=repository,
            check=True,
            capture_output=True,
        ).stdout
        inventory = VERIFY_RELEASE.repository_source_inventory(repository)

        self.assertEqual(
            inventory["scripts/check-version.sh"],
            (hashlib.sha256(blob).hexdigest(), 0o755),
        )


if __name__ == "__main__":
    unittest.main()
