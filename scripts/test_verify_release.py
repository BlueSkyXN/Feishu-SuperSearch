from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


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


if __name__ == "__main__":
    unittest.main()
