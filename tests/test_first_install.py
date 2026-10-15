"""Run without root, NVIDIA hardware, or modifications to system directories."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class FirstInstallTests(unittest.TestCase):
    def test_requires_initramfs(self):
        result = subprocess.run(["bash", str(ROOT / "install.sh"), "--disable-nouveau"],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--disable-nouveau requires --initramfs", result.stderr)

    def check_version(self, versions, expected=0, missing=None):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            lines = []
            for library, version in versions:
                target = base / f"{library}.so.{version}"
                target.touch()
                link = base / f"{library}-{len(lines)}.so.1"
                link.symlink_to(target)
                if library != missing:
                    lines.append(f"{library}.so.1 (libc6,x86-64) => {link}")
            for executable in ("nvidia-smi", "nvidia-modprobe"):
                script = base / executable
                # Ensure the version check never tries to initialize NVML.
                script.write_text("#!/bin/sh\nexit 99\n")
                script.chmod(0o755)
            mock = base / "ldconfig"
            mock.write_text("#!/bin/sh\nprintf '%s\\n' \"$CMP50_TEST_LDCONFIG\"\n")
            mock.chmod(0o755)
            env = dict(os.environ, PATH=f"{base}:{os.environ['PATH']}",
                       CMP50_TEST_LDCONFIG="\n".join(lines))
            result = subprocess.run(["bash", "-c",
                'source "$1"; cmp50_userspace_matches 610.43.03',
                "test", str(ROOT / "scripts/userspace-version.sh")], env=env)
            self.assertEqual(result.returncode, expected)

    def test_first_install_does_not_need_live_driver(self):
        self.check_version([("libnvidia-ml", "610.43.03"), ("libcuda", "610.43.03")])

    def test_rejects_mismatched_libraries(self):
        self.check_version([("libnvidia-ml", "610.43.03"), ("libcuda", "615.0.0")], 1)

    def test_rejects_missing_library(self):
        self.check_version([("libnvidia-ml", "610.43.03")], 1)

    def test_does_not_accept_shadowed_matching_version(self):
        self.check_version([("libnvidia-ml", "615.0.0"),
                            ("libnvidia-ml", "610.43.03"), ("libcuda", "610.43.03")], 1)


if __name__ == "__main__":
    unittest.main()
