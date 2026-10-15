import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class Gen2CleanupTests(unittest.TestCase):
    def run_cleanup(self, commands):
        # Only definitions/traps are evaluated, never the hardware/root block.
        source = (ROOT / "scripts/cmp50-gen2-second-pass.sh").read_text()
        prefix, _ = source.split('\n[[ ${EUID} -eq 0 ]]', 1)
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            mock = base / "systemctl"
            mock.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$CMP50_UNIT_LOG"\n')
            mock.chmod(0o755)
            env = dict(os.environ, PATH=f"{base}:{os.environ['PATH']}",
                       CMP50_UNIT_LOG=str(base / "units"))
            result = subprocess.run(["bash"], input=prefix + "\n" + commands,
                                    env=env, capture_output=True, text=True)
            lines = (base / "units").read_text().splitlines() if (base / "units").exists() else []
            return result, lines

    def assert_restored(self, lines):
        self.assertTrue(lines[0].startswith("unmask --runtime systemd-udevd.service"))
        self.assertEqual(lines[1], "daemon-reload")
        self.assertTrue(lines[2].startswith("start systemd-udevd-control.socket"))

    def test_does_not_touch_udev_before_masking(self):
        result, lines = self.run_cleanup("exit 0\n")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(lines, [])

    def test_restores_udev_on_partial_operation_failure(self):
        result, lines = self.run_cleanup("udev_stopped=1\nfalse\n")
        self.assertNotEqual(result.returncode, 0)
        self.assert_restored(lines)

    def test_restores_udev_on_sigterm(self):
        result, lines = self.run_cleanup("udev_stopped=1\nkill -TERM $$\n")
        self.assertEqual(result.returncode, 143)
        self.assert_restored(lines)


if __name__ == "__main__":
    unittest.main()
