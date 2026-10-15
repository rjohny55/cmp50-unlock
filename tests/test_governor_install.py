"""Installer tests use a temporary filesystem and fake hardware/systemd only."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class GovernorInstallTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.repo = self.base / "repo"
        (self.repo / "scripts").mkdir(parents=True)
        self.sysfs = self.base / "pci"
        self.sysfs.mkdir()
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}")
        self.mock("uname", 'case "$1" in -r) echo 6.8.0-147-generic;; *) echo x86_64;; esac')
        self.set_cards(2)
        helper = (ROOT / "scripts/governor-profile.sh").read_text().replace(
            "/sys/bus/pci/devices", str(self.sysfs))
        (self.repo / "scripts/governor-profile.sh").write_text(helper)

    def mock(self, name, content):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + content + "\n")
        path.chmod(0o755)

    def set_cards(self, count, mixed=False):
        lines = []
        for i in range(count):
            bdf = f"0000:{i + 1:02x}:00.0"
            directory = self.sysfs / bdf
            directory.mkdir(exist_ok=True)
            (directory / "subsystem_vendor").write_text("0x10de\n")
            (directory / "subsystem_device").write_text("0x1554\n")
            lines.append(f"{bdf} 0302: 10de:1e09 (rev a1)")
        if mixed:
            lines.append("0000:80:00.0 0300: 10de:2330")
        # Non-GPU NVIDIA functions must not count as mixed GPUs.
        lines.append("0000:80:00.1 0403: 10de:1234")
        self.mock("lspci", "printf '%s\\n' '" + "\n".join(lines) + "'")

    def profile(self):
        return subprocess.run(["bash", "-c", 'source "$1"; cmp50_governor_profile',
                               "test", str(self.repo / "scripts/governor-profile.sh")],
                              env=self.env, capture_output=True, text=True)

    def test_auto_profiles(self):
        for count in (1, 2, 4, 6, 8):
            self.set_cards(count)
            result = self.profile()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), f"cmp50-{count}x20gb")

    def test_rejects_mixed_gpus(self):
        self.set_cards(2, mixed=True)
        self.assertNotEqual(self.profile().returncode, 0)

    def test_rejects_unsupported_count_and_subsystem(self):
        self.set_cards(3)
        self.assertNotEqual(self.profile().returncode, 0)
        self.set_cards(2)
        (self.sysfs / "0000:01:00.0/subsystem_device").write_text("0xbeef\n")
        self.assertNotEqual(self.profile().returncode, 0)

    def prepare_installer(self):
        # Rewrite system destinations in the test copy; never touch the host.
        text = (ROOT / "install-governor.sh").read_text()
        text = text.replace('[[ ${EUID} -eq 0 ]]', '[[ 0 -eq 0 ]]')
        for path in ("/run/lock", "/usr/local/sbin", "/usr/local/libexec", "/etc"):
            (self.base / path.lstrip("/")).mkdir(parents=True, exist_ok=True)
            text = text.replace(path, str(self.base / path.lstrip("/")))
        (self.base / "etc/systemd/system").mkdir(parents=True, exist_ok=True)
        (self.base / "etc/default").mkdir(parents=True, exist_ok=True)
        (self.repo / "install-governor.sh").write_text(text)
        bundle = self.repo / "idle-governor"
        (bundle / "bin").mkdir(parents=True)
        binary = bundle / "bin/cmp-idle-governor-linux-amd64"
        binary.write_text("#!/bin/sh\necho 1.0.1\n")
        binary.chmod(0o755)
        (bundle / "checksums.sha256").touch()
        shutil.copyfile(ROOT / "idle-governor/cmp-idle-governor.service", bundle / "cmp-idle-governor.service")
        self.mock("sha256sum", "exit 0")
        self.mock("chown", "exit 0")
        self.mock("sleep", "exit 0")
        self.mock("nvidia-smi", "printf '610.43.03\\n610.43.03\\n'")
        self.unit_log = self.base / "unit-log"
        self.mock("systemctl", f'''printf '%s\\n' "$*" >> '{self.unit_log}'
case "$1" in cat|is-active) exit 1;; esac''')
        self.data = self.base / "data"
        self.data.mkdir()

    def run_installer(self, *extra):
        return subprocess.run(["bash", str(self.repo / "install-governor.sh"),
                               "--ai-manager-data-dir", str(self.data), *extra],
                              env=self.env, capture_output=True, text=True)

    def test_creates_enabled_control_and_enables_service_without_driver_writes(self):
        self.prepare_installer()
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        control = json.loads((self.data / "cmp50-power/control.json").read_text())
        self.assertEqual(control, dict(enabled=True, profile="cmp50-2x20gb", idle_fan_percent=0))
        calls = self.unit_log.read_text()
        self.assertIn("enable cmp-idle-governor.service", calls)
        self.assertNotIn("start cmp-idle-governor", calls)
        self.assertNotIn("gen2", calls)

    def test_preserves_existing_disabled_control_and_environment(self):
        self.prepare_installer()
        control = self.data / "cmp50-power/control.json"
        control.parent.mkdir()
        original = '{"enabled":false,"profile":"cmp50-2x20gb","idle_fan_percent":25}\n'
        control.write_text(original)
        env = self.base / "etc/default/cmp-idle-governor"
        env.parent.mkdir(exist_ok=True)
        settings = f"CMP50_POWER_CONTROL_DIR={control.parent}\nCMP_POLL=10\n"
        env.write_text(settings)
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(control.read_text(), original)
        self.assertEqual(env.read_text(), settings)

    def test_rejects_unexecutable_binary_before_installing_service(self):
        self.prepare_installer()
        (self.repo / "idle-governor/bin/cmp-idle-governor-linux-amd64").write_text("#!/missing-loader\n")
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.unit_log.exists())

    def test_start_now_reports_startup_failure(self):
        self.prepare_installer()
        result = self.run_installer("--start-now")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("governor failed to start", result.stderr)
        self.assertIn("start cmp-idle-governor.service", self.unit_log.read_text())

    def test_start_now_rejects_wrong_live_driver_before_writes(self):
        self.prepare_installer()
        self.mock("nvidia-smi", "echo 615.0.0")
        result = self.run_installer("--start-now")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("610.43.03 driver", result.stderr)
        self.assertFalse(self.unit_log.exists())
        self.assertFalse((self.data / "cmp50-power").exists())

    def test_all_in_one_keeps_explicit_acknowledgement(self):
        result = subprocess.run(["bash", str(ROOT / "install-all.sh")],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--allow-experimental-gen2", result.stderr)

    def test_all_in_one_stages_correct_components_and_never_reboots(self):
        self.prepare_installer()
        source = (ROOT / "install-all.sh").read_text().replace(
            '[[ ${EUID} -eq 0 ]]', '[[ 0 -eq 0 ]]')
        source = source.replace("/run/lock", str(self.base / "run/lock"))
        wrapper = self.repo / "install-all.sh"
        wrapper.write_text(source)
        (self.repo / "scripts/userspace-version.sh").write_text(
            "cmp50_userspace_matches() { return 1; }\n")
        calls = self.base / "child-installs"
        self.mock("bash", f"printf '%s\\n' \"$*\" >> '{calls}'")
        result = subprocess.run(["/bin/bash", str(wrapper), "--allow-experimental-gen2",
                                 "--ai-manager-data-dir", str(self.data)],
                                env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = calls.read_text().splitlines()
        self.assertEqual(len(commands), 2)
        self.assertIn("install.sh --stage gen2 --allow-experimental-gen2 --initramfs --disable-nouveau --install-userland", commands[0])
        self.assertIn("install-governor.sh --ai-manager-data-dir", commands[1])
        self.assertNotIn("--start-now", commands[1])
        self.assertFalse(self.unit_log.exists())

    def test_all_in_one_stops_when_driver_installer_fails(self):
        self.prepare_installer()
        source = (ROOT / "install-all.sh").read_text().replace(
            '[[ ${EUID} -eq 0 ]]', '[[ 0 -eq 0 ]]').replace("/run/lock", str(self.base / "run/lock"))
        wrapper = self.repo / "install-all.sh"
        wrapper.write_text(source)
        (self.repo / "scripts/userspace-version.sh").write_text("cmp50_userspace_matches() { return 0; }\n")
        calls = self.base / "child-installs"
        self.mock("bash", f"printf '%s\\n' \"$*\" >> '{calls}'; exit 42")
        result = subprocess.run(["/bin/bash", str(wrapper), "--allow-experimental-gen2"],
                                env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 42)
        self.assertEqual(len(calls.read_text().splitlines()), 1)


if __name__ == "__main__":
    unittest.main()
