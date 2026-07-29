from __future__ import annotations

import importlib.util
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("check-third-party-licenses.py")
SPEC = importlib.util.spec_from_file_location("check_third_party_licenses", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
LICENSE_CHECK = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = LICENSE_CHECK
SPEC.loader.exec_module(LICENSE_CHECK)


class LicenseInventoryTest(unittest.TestCase):
    def write_license(self, root: Path, relative: str, content: str = "license\n") -> Path:
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def repository_git_root(self) -> Path:
        if not shutil.which("git"):
            self.skipTest("git is required for exact-root inventory tests")
        root = SCRIPT.parent.parent.resolve()
        result = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0 or Path(result.stdout.strip()).resolve() != root:
            self.skipTest("test requires an exact Git checkout root")
        return root

    def test_non_git_source_tree_uses_filesystem_inventory(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            self.write_license(root, "LICENSES/runtime/dependency.txt")

            with mock.patch.object(LICENSE_CHECK, "ROOT", root):
                actual = LICENSE_CHECK.tracked_license_files()

            self.assertEqual(actual, {"LICENSES/runtime/dependency.txt"})

    def test_missing_git_uses_filesystem_inventory(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            self.write_license(root, "LICENSES/runtime/dependency.txt")

            with (
                mock.patch.object(LICENSE_CHECK, "ROOT", root),
                mock.patch.object(LICENSE_CHECK.subprocess, "run", side_effect=FileNotFoundError("git")),
            ):
                actual = LICENSE_CHECK.tracked_license_files()

            self.assertEqual(actual, {"LICENSES/runtime/dependency.txt"})

    def test_nested_source_tree_does_not_use_outer_git_index(self) -> None:
        outer = self.repository_git_root()
        with tempfile.TemporaryDirectory(dir=outer) as temporary:
            root = Path(temporary) / "source"
            self.write_license(root, "LICENSES/runtime/filesystem-only.txt")

            with mock.patch.object(LICENSE_CHECK, "ROOT", root):
                actual = LICENSE_CHECK.tracked_license_files()

            self.assertEqual(actual, {"LICENSES/runtime/filesystem-only.txt"})

    def test_extra_archived_license_file_fails_inventory_check(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            known = {"LICENSES/runtime/dependency.txt"}
            for relative in known | LICENSE_CHECK.PROJECT_LICENSE_FILES | {"LICENSES/extra.txt"}:
                self.write_license(root, relative)

            manifest = {"license_files": object(), "go_modules": object(), "npm_packages": object()}

            def validate_entries(
                label: str, raw: object, known_files: set[str]
            ) -> tuple[dict[str, str], set[str]]:
                del raw
                return ({}, set(known_files)) if label == "go_modules" else ({}, set())

            with (
                mock.patch.object(LICENSE_CHECK, "ROOT", root),
                mock.patch.object(LICENSE_CHECK, "load_manifest", return_value=manifest),
                mock.patch.object(LICENSE_CHECK, "validate_license_files", return_value=known),
                mock.patch.object(LICENSE_CHECK, "validate_entries", side_effect=validate_entries),
                mock.patch.object(
                    LICENSE_CHECK,
                    "actual_go_modules",
                    side_effect=AssertionError("Go inventory must not run"),
                ),
                mock.patch.object(
                    LICENSE_CHECK,
                    "actual_npm_packages",
                    side_effect=AssertionError("npm inventory must not run"),
                ),
            ):
                with self.assertRaisesRegex(
                    SystemExit,
                    r"tracked LICENSES inventory drift; .*extra=\['LICENSES/extra\.txt'\]",
                ):
                    LICENSE_CHECK.main()

    def test_rejects_symlinked_licenses_directory(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            temporary_root = Path(temporary)
            root = temporary_root / "source"
            root.mkdir()
            target = temporary_root / "licenses-target"
            target.mkdir()
            try:
                (root / "LICENSES").symlink_to(target, target_is_directory=True)
            except OSError as error:
                self.skipTest(f"symlink creation is unavailable: {error}")

            with mock.patch.object(LICENSE_CHECK, "ROOT", root):
                with self.assertRaisesRegex(SystemExit, r"LICENSES must be a regular directory"):
                    LICENSE_CHECK.tracked_license_files()

    def test_rejects_symlink_inside_archived_licenses(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            target = self.write_license(root, "LICENSES/target.txt")
            link = root / "LICENSES/link.txt"
            try:
                link.symlink_to(target)
            except OSError as error:
                self.skipTest(f"symlink creation is unavailable: {error}")

            with mock.patch.object(LICENSE_CHECK, "ROOT", root):
                with self.assertRaisesRegex(SystemExit, r"symlink: LICENSES/link\.txt"):
                    LICENSE_CHECK.tracked_license_files()

    def test_exact_git_root_uses_tracked_inventory(self) -> None:
        root = self.repository_git_root()
        manifest = LICENSE_CHECK.load_manifest()
        expected = set(manifest["license_files"]) | LICENSE_CHECK.PROJECT_LICENSE_FILES

        with (
            mock.patch.object(LICENSE_CHECK, "ROOT", root),
            mock.patch.object(
                LICENSE_CHECK,
                "archived_license_files",
                side_effect=AssertionError("filesystem inventory must not run"),
            ),
        ):
            actual = LICENSE_CHECK.tracked_license_files()

        self.assertEqual(actual, expected)


if __name__ == "__main__":
    unittest.main()
