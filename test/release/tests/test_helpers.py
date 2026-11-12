"""Offline regressions for the supervised Unix release kit."""

import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest


KIT = Path(__file__).resolve().parents[1]


class HelperTests(unittest.TestCase):
    def test_ctrl_c_works_when_launcher_inherited_ignored_signals(self):
        def ignore_terminal_signals():
            signal.signal(signal.SIGINT, signal.SIG_IGN)
            signal.signal(signal.SIGQUIT, signal.SIG_IGN)

        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run(
                [sys.executable, "-I", str(KIT / "lib/ptyctl.py"),
                 "--log", str(Path(directory) / "session.log"),
                 "--step", "wait:Ready:5", "--step", "intr", "--step", "exit:2",
                 "--", sys.executable, "-u", "-c",
                 "import time; print('Ready', flush=True); time.sleep(60)"],
                preexec_fn=ignore_terminal_signals,
                capture_output=True, text=True, timeout=10,
            )
            self.assertEqual(result.returncode, 130, result.stdout + result.stderr)
            self.assertLess(json.loads(result.stdout)["seconds_after_last_intr"], 2)

    def test_pty_has_renderable_terminal_dimensions(self):
        with tempfile.TemporaryDirectory() as directory:
            transcript = Path(directory) / "terminal.log"
            result = subprocess.run(
                [sys.executable, "-I", str(KIT / "lib/ptyctl.py"),
                 "--log", str(transcript), "--", sys.executable, "-c",
                 "import json,os; print(json.dumps(list(os.get_terminal_size())))"],
                capture_output=True, text=True, timeout=10, check=True,
            )
            self.assertEqual(json.loads(transcript.read_text()), [120, 24])
            self.assertEqual(json.loads(result.stdout)["exit"], 0)

    def test_sampler_shutdown_does_not_contaminate_following_check(self):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run(
                ["bash", "-c", '''
. "$1"
rt_init fixture
( while :; do : "Screen Sharing"; done ) &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true; wait "$sampler" 2>/dev/null || true' EXIT
sleep 0.05
rt_stop_sampler "$sampler" || exit $?
rt_check NEXT "clean output" --nomatch 'Screen Sharing' -- printf '%s\\n' qemu-system-aarch64
''', "test", str(KIT / "lib/rt.sh")],
                env={**os.environ, "RT_RUN_DIR": directory},
                capture_output=True, text=True, timeout=10,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn("Screen Sharing", result.stderr)
            self.assertNotIn("Screen Sharing", (Path(directory) / "fixture/logs/NEXT.log").read_text())

    def test_invocation_settings_override_file_including_empty_values(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            kit = root / "test/release"
            (kit / "lib").mkdir(parents=True)
            shutil.copyfile(KIT / "unix.sh", kit / "unix.sh")
            shutil.copyfile(KIT / "lib/rt.sh", kit / "lib/rt.sh")
            (kit / "release.env").write_text(
                "RT_TARGET=ubuntu/server/arm64\nRT_RUN_DIR=/wrong\n"
                "RT_ALLOW_INSECURE_CREDENTIALS=1\nRT_PULL_REF=utm-image\n"
            )
            binary = root / "sailwright"
            binary.write_text(
                '#!/bin/sh\nprintf "%s\\n" "$RT_TARGET" "$RT_RUN_DIR" '
                '"risk=$RT_ALLOW_INSECURE_CREDENTIALS" "pull=$RT_PULL_REF"\n'
            )
            binary.chmod(0o755)
            result = subprocess.run(
                ["bash", str(kit / "unix.sh"), "__sw"],
                env={**os.environ, "RT_BIN_DIR": directory,
                     "RT_TARGET": "macos//arm64", "RT_RUN_DIR": str(root / "out"),
                     "RT_ALLOW_INSECURE_CREDENTIALS": "", "RT_PULL_REF": ""},
                capture_output=True, text=True, timeout=10, check=True,
            )
            self.assertEqual(result.stdout.splitlines(),
                             ["macos//arm64", str(root / "out"), "risk=", "pull="])

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
                # macOS ps prints only the first line of awk's multiline program.
                "109 109 S awk -v pattern /release/run",
                "110 109 S /usr/bin/awk -v pattern /release/run",
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

    def _wait_lock_holder(self, locks, script):
        # The watched process writes its holder record (or not) and then lingers briefly.
        return subprocess.run(
            ["bash", "-c", '''
. "$1"
locks=$2
( eval "$3"; sleep 1 ) &
watched=$!
rt_wait_lock_holder "$locks" "$watched" 100
status=$?
wait "$watched"
exit "$status"
''', "test", str(KIT / "lib/rt.sh"), str(locks), script],
            capture_output=True, text=True, timeout=15,
        )

    def test_lock_wait_ignores_stale_holder_of_another_process(self):
        with tempfile.TemporaryDirectory() as directory:
            locks = Path(directory) / "locks"
            locks.mkdir()
            # A stop that was killed never truncates its record.
            (locks / "stale.lock").write_text(
                '{"pid":999999,"program":"sailwright","operation":"stop","target":"t"}')
            result = self._wait_lock_holder(locks, ":")
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)

    def test_lock_wait_sees_holder_record_of_watched_process(self):
        with tempfile.TemporaryDirectory() as directory:
            locks = Path(directory) / "locks"
            locks.mkdir()
            (locks / "other.lock").write_text(
                '{"pid":1,"program":"sailwright","operation":"stop"}')
            result = self._wait_lock_holder(locks, '''
printf '{"pid":%s,"program":"sailwright","operation":"stop"}' "$BASHPID" >"$locks/t.lock"
''')
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
