import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class Gen2KernelGateTests(unittest.TestCase):
    def run_install(self, kernel):
        with tempfile.TemporaryDirectory() as directory:
            mock = Path(directory) / "uname"
            mock.write_text('#!/bin/sh\nprintf "%s\\n" "$CMP50_TEST_KERNEL"\n')
            mock.chmod(0o755)
            env = dict(os.environ, PATH=f"{directory}:{os.environ['PATH']}",
                       CMP50_TEST_KERNEL=kernel)
            # The invalid data path (or the unprivileged EUID) stops execution
            # before hardware lookup/backups even on a root CI runner.
            return subprocess.run(["bash", str(ROOT / "install.sh"), "--stage", "gen2",
                "--allow-experimental-gen2", "--ai-manager-data-dir", "relative"],
                env=env, capture_output=True, text=True)

    def test_kernel_138_remains_blocked(self):
        result = self.run_install("6.8.0-138-generic")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("supports only kernels", result.stderr)

    def test_unknown_new_kernel_remains_blocked(self):
        result = self.run_install("7.0.0-1-generic")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("supports only kernels", result.stderr)

    def test_exact_kernel_147_is_admitted_to_preflight(self):
        result = self.run_install("6.8.0-147-generic")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("supports only kernels", result.stderr)

    def test_exact_kernel_137_is_still_admitted_to_preflight(self):
        result = self.run_install("6.8.0-137-generic")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("supports only kernels", result.stderr)


if __name__ == "__main__":
    unittest.main()
