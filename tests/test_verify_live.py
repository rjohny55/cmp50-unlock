import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class VerifyLiveTests(unittest.TestCase):
    def run_verifier(self, fail_second=False):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            (base / "verify").mkdir()
            (base / "artifacts/test").mkdir(parents=True)
            (base / "bin").mkdir()
            shutil.copy(ROOT / "verify-live.sh", base)
            shutil.copy(ROOT / "verify/verify.py", base / "verify")
            controls = {
                "v1": {"rm_status": "0x00000000", "values": [{"raw": 0}] * 9},
                "v2": {"rm_status": "0x00000000", "values": [{"raw": 0}] * 8},
                "gpu_info": {"values": [{"name": "CMP_SKU", "raw": 1},
                                          {"name": "DISPLAY_ENABLED", "raw": 0}]},
                "gr_info": {"values": [{"name": name, "raw": value} for name, value in
                    (("GPU_CORE_COUNT", 3584), ("RT_CORE_COUNT", 56), ("TENSOR_CORE_COUNT", 448))]},
            }
            (base / "report.json").write_text(json.dumps({"controls": controls}))
            scripts = {
                base / "bin/nvidia-smi": """#!/bin/sh
case "$*" in
  *index,pci.device_id*) printf '0, 0x1E0910DE\\n2, 0x1E0910DE\\n' ;;
  *--id=0,2*--query-gpu=memory.total*) printf '20480\\n20480\\n' ;;
  *--query-gpu=memory.total*) exit 7 ;;
  *) printf 'GPU list\\n' ;;
esac
""",
                base / "bin/systemctl": "#!/bin/sh\nexit 1\n",
                base / "artifacts/test/rm-issue-rate": """#!/bin/sh
printf '%s\\n' "$1" >> "$CMP50_FIXTURE/calls"
if [ "$CMP50_FAIL_SECOND" = 1 ] && [ "$1" = 2 ]; then exit 9; fi
cat "$CMP50_FIXTURE/report.json"
""",
            }
            for path, source in scripts.items():
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=f"{base}/bin:{os.environ['PATH']}",
                       CMP50_FIXTURE=str(base), CMP50_FAIL_SECOND=str(int(fail_second)))
            result = subprocess.run(["bash", str(base / "verify-live.sh")],
                                    env=env, capture_output=True, text=True)
            self.assertTrue((base / "calls").exists(), result.stdout + result.stderr)
            self.assertEqual((base / "calls").read_text().splitlines(), ["0", "2"])
            return result

    def test_checks_every_cmp_card_in_mixed_gpu_system(self):
        result = self.run_verifier()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("PASS_CMP50_RM: GPU 2", result.stdout)
        self.assertIn("PASS_CMP50_LIVE: 2 card(s) visible", result.stdout)

    def test_second_gpu_failure_is_not_reported_as_success(self):
        result = self.run_verifier(fail_second=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("PASS_CMP50_LIVE", result.stdout)


if __name__ == "__main__":
    unittest.main()
