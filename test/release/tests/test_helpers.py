"""Offline regressions for the supervised Unix release kit."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


KIT = Path(__file__).resolve().parents[1]


class HelperTests(unittest.TestCase):
    def test_ctrl_c_transcript_is_assertable_separately_from_json(self):
        with tempfile.TemporaryDirectory() as directory:
            transcript = Path(directory) / "session.log"
            child = (
                "import signal,time,sys; "
                "signal.signal(signal.SIGINT, lambda *_: "
                "(print('Received interrupt', flush=True), sys.exit(3))); "
                "print('Ready', flush=True); time.sleep(60)"
            )
            result = subprocess.run(
                [sys.executable, "-I", str(KIT / "lib/ptyctl.py"),
                 "--log", str(transcript), "--step", "wait:Ready:5",
                 "--step", "intr", "--step", "exit:5", "--",
                 sys.executable, "-u", "-c", child],
                capture_output=True, text=True, timeout=15,
            )
            self.assertEqual(result.returncode, 3, result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["exit"], 3)
            self.assertLess(report["seconds_after_last_intr"], 5)
            self.assertIn("Received interrupt", result.stderr)
            self.assertEqual(transcript.read_text(), result.stderr)

    def test_process_filter_keeps_build_orphans_but_excludes_filesystem_probes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake_ps = root / "ps"
            fake_ps.write_text('#!/bin/sh\nprintf "%s\\n" "$TEST_PROCESS_LIST"\n')
            fake_ps.chmod(0o755)
            processes = [
                "101 100 S /opt/bin/packer build -var cache_dir=/release/run/appdata",
                "102 100 R /opt/bin/qemu-system-aarch64 -drive file=/release/run/disk",
                "103 103 S /opt/bin/limactl shell --instance colima stat /release/run/file",
                "104 103 S limactl shell --instance colima sudo chmod 644 /release/run/file",
                "105 105 S bash /repo/test/release/unix.sh interrupt /release/run",
                "106 106 S python3 /repo/test/release/lib/ptyctl.py --log /release/run/log",
                "107 107 S packer build -var cache_dir=/unrelated/run",
                "108 108 S sailwright-gateway --state-dir /release/run/gateway",
            ]
            result = subprocess.run(
                ["bash", "-c", '. "$1"; rt_processes_matching "$2"',
                 "test", str(KIT / "lib/rt.sh"), "/release/run"],
                env={**os.environ, "PATH": f"{root}:{os.environ['PATH']}",
                     "TEST_PROCESS_LIST": "\n".join(processes)},
                capture_output=True, text=True, timeout=5, check=True,
            )
            self.assertEqual(result.stdout.splitlines(),
                             [processes[0], processes[1], processes[7]])


if __name__ == "__main__":
    unittest.main()
