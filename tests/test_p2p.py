"""Offline safety and BAR1 regression checks; never access live hardware."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class P2PTests(unittest.TestCase):
    def test_build_rejects_untested_kernels_before_source_access(self):
        for kernel in ("6.8.0-137-generic", "6.8.0-138-generic", "7.0.0-1-generic"):
            with self.subTest(kernel=kernel):
                result = subprocess.run(["bash", str(ROOT / "build-p2p.sh")],
                    env=dict(os.environ, KERNEL_RELEASE=kernel), capture_output=True, text=True)
                self.assertEqual(result.returncode, 2)
                self.assertIn("Only kernel 6.8.0-147-generic", result.stderr)

    def test_build_rejects_wrong_archive_before_extraction(self):
        with tempfile.NamedTemporaryFile() as archive:
            result = subprocess.run(["bash", str(ROOT / "build-p2p.sh"),
                "--source-tarball", archive.name], env=dict(os.environ,
                KERNEL_RELEASE="6.8.0-147-generic"), capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("FAILED", result.stdout)
            self.assertNotIn("P2P_BUILD_PASS", result.stdout)

    def test_helper_calculates_16_and_32_gib_and_rejects_other_selectors(self):
        source = (ROOT / "scripts/cmp50-gen2-second-pass.sh").read_text()
        start = source.index('case "$bar1_selector"')
        end = source.index("\nnvidia-modprobe", start)
        fragment = source[start:end]
        for selector, expected in ((8, "14 16384"), (9, "15 32768"), (7, None), (10, None)):
            with self.subTest(selector=selector):
                result = subprocess.run(["bash", "-c", 'set -eu; die() { exit 2; }; '
                    f"bar1_selector={selector}; " + fragment +
                    '\nprintf "%s %s" "$expected_rebar_encoding" "$expected_bar1_mib"'],
                    capture_output=True, text=True)
                self.assertEqual(result.returncode, 0 if expected else 2)
                if expected:
                    self.assertEqual(result.stdout, expected)

    def test_dma32_patch_keeps_fail_closed_address_checks(self):
        patch = (ROOT / "08-cmp50-dma32-bootstrap.patch").read_text()
        added = "\n".join(line[1:] for line in patch.splitlines()
                          if line.startswith("+") and not line.startswith("+++"))
        self.assertEqual(added.count("MEMDESC_FLAGS_ALLOC_32BIT_ADDRESSABLE"), 3)
        self.assertEqual(added.count("NV_ERR_INVALID_ADDRESS"), 3)
        self.assertEqual(added.count("NV_U32_MAX"), 3)


if __name__ == "__main__":
    unittest.main()
