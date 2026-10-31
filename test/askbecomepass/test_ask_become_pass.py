"""End-to-end tests for `sailwright provision local --ask-become-pass` (-K).

They run the real binary in a pseudo-terminal, like a person in a terminal
would, and answer Ansible's become prompt. The prompt has no trailing newline,
so it only shows up if Ansible writes it to the terminal (/dev/tty) and not
through Sailwright's line-by-line log prefixing. If that breaks, the prompt
never appears and the tests time out with a clear message.

The tests change the host (sudo writes a root-owned marker file and may
install a package), so they only run when SAILWRIGHT_ASKPASS_BIN is set.
Run them as a user whose sudo needs a password, see
.github/workflows/test-ask-become-pass.yml:

    SAILWRIGHT_ASKPASS_BIN=/path/to/sailwright \
    SAILWRIGHT_ASKPASS_PASSWORD=<sudo password of the current user> \
    SAILWRIGHT_ASKPASS_FIXTURE=test/askbecomepass/fixture \
    python3 -m unittest discover -s test/askbecomepass -v

Optional settings:
    SAILWRIGHT_ASKPASS_WORKDIR      directory outside any git checkout to run in
                                    (default: a new temporary directory)
    SAILWRIGHT_ASKPASS_APT_PACKAGE  package the play installs with apt (Linux)
    SAILWRIGHT_ASKPASS_PROMPT_TIMEOUT  seconds until the prompt must appear (180)
    SAILWRIGHT_ASKPASS_RUN_TIMEOUT     seconds for a whole run (1200)
"""

import errno
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import subprocess
import tempfile
import time
import unittest
import uuid


BIN = os.environ.get("SAILWRIGHT_ASKPASS_BIN", "")
PASSWORD = os.environ.get("SAILWRIGHT_ASKPASS_PASSWORD", "")
FIXTURE = Path(
    os.environ.get(
        "SAILWRIGHT_ASKPASS_FIXTURE", Path(__file__).resolve().parent / "fixture"
    )
).resolve()
APT_PACKAGE = os.environ.get("SAILWRIGHT_ASKPASS_APT_PACKAGE", "")
PROMPT_TIMEOUT = float(os.environ.get("SAILWRIGHT_ASKPASS_PROMPT_TIMEOUT", "180"))
RUN_TIMEOUT = float(os.environ.get("SAILWRIGHT_ASKPASS_RUN_TIMEOUT", "1200"))
MARKER = Path("/etc/sailwright-become-probe")

# Ansible's prompt, at the start of a line (so not behind a log prefix) and
# waiting for input (nothing after it). Older Ansible versions said SUDO.
PROMPT_AT_LINE_START = re.compile(r"(?:\A|[\r\n])(?:BECOME|SUDO) password: ?\Z")
PROMPT_ANYWHERE = re.compile(r"(?:BECOME|SUDO) password:")
RECAP_OK = re.compile(r"\blocalhost\s*:\s*ok=\d+.*\bunreachable=0\s+failed=0\b")
WRONG_PASSWORD = re.compile(r"(?i)incorrect (?:sudo|become) password|sorry, try again")
# Sailwright forces colored Ansible output.
ANSI_ESCAPE = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")


def strip_ansi(text):
    return ANSI_ESCAPE.sub("", text)


class PtyRun:
    """Result of a command run in a pseudo-terminal."""

    def __init__(self, exit_code, transcript, prompts, timed_out_reason):
        self.exit_code = exit_code
        self.transcript = transcript
        self.plain = strip_ansi(transcript)
        self.prompts = prompts
        self.timed_out_reason = timed_out_reason


def run_in_pty(argv, env, cwd, answer=None, max_prompts=1):
    """Run argv with a pseudo-terminal as its controlling terminal.

    Answers each become prompt with `answer`. Stops the run if a prompt does
    not appear within PROMPT_TIMEOUT (when `answer` is set), if more than
    `max_prompts` prompts appear, or after RUN_TIMEOUT.
    """
    pid, fd = pty.fork()
    if pid == 0:  # child: session leader with the pty as controlling terminal
        try:
            os.chdir(cwd)
            os.execvpe(argv[0], argv, env)
        finally:
            os._exit(127)

    started = time.monotonic()
    chunks = []
    scan_from = 0
    prompts = 0
    reason = None
    while True:
        elapsed = time.monotonic() - started
        if elapsed > RUN_TIMEOUT:
            reason = f"run took longer than {RUN_TIMEOUT:.0f}s"
            break
        if answer is not None and prompts == 0 and elapsed > PROMPT_TIMEOUT:
            reason = f"no become prompt at the start of a line within {PROMPT_TIMEOUT:.0f}s"
            break
        ready, _, _ = select.select([fd], [], [], 1.0)
        if not ready:
            continue
        try:
            data = os.read(fd, 65536)
        except OSError as err:  # Linux reports EIO once the child is gone
            if err.errno != errno.EIO:
                raise
            data = b""
        if not data:
            break
        chunks.append(data.decode("utf-8", errors="replace"))
        text = "".join(chunks)
        # Only the tail can hold a prompt that waits for input.
        tail = strip_ansi(text[max(scan_from, len(text) - 4096):])
        if PROMPT_AT_LINE_START.search(tail):
            prompts += 1
            scan_from = len(text)
            if prompts > max_prompts:
                reason = f"asked {prompts} times for the become password"
                break
            os.write(fd, ((answer or "") + "\n").encode())

    if reason is not None:
        for sig in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(pid, sig)
            except ProcessLookupError:
                break
            time.sleep(2)
        try:  # collect what was still buffered
            while select.select([fd], [], [], 0.5)[0]:
                data = os.read(fd, 65536)
                if not data:
                    break
                chunks.append(data.decode("utf-8", errors="replace"))
        except OSError:
            pass

    _, status = os.waitpid(pid, 0)
    os.close(fd)
    return PtyRun(os.waitstatus_to_exitcode(status), "".join(chunks), prompts, reason)


@unittest.skipUnless(BIN, "set SAILWRIGHT_ASKPASS_BIN to run the --ask-become-pass end-to-end tests")
class AskBecomePassTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not PASSWORD:
            raise unittest.SkipTest("SAILWRIGHT_ASKPASS_PASSWORD is not set")
        if os.geteuid() == 0:
            raise RuntimeError("run as an unprivileged user whose sudo needs a password")
        probe = subprocess.run(["sudo", "-n", "true"], capture_output=True, check=False)
        if probe.returncode == 0:
            raise RuntimeError(
                "sudo works without a password for this user; the test would not "
                "prove that the prompt works"
            )
        if not (FIXTURE / "ansible-role-sources.yml").is_file():
            raise RuntimeError(f"fixture not found: {FIXTURE}")

        workdir = os.environ.get("SAILWRIGHT_ASKPASS_WORKDIR")
        if workdir:
            cls.workdir = Path(workdir)
            cls.workdir.mkdir(parents=True, exist_ok=True)
        else:
            cls._temp = tempfile.TemporaryDirectory(prefix="sailwright-askpass-")
            cls.workdir = Path(cls._temp.name)
        # The binary must not find a Sailwright checkout from here, so it uses
        # its embedded project like a released binary does.
        state = cls.workdir / "state"
        state.mkdir(exist_ok=True)

        env = dict(os.environ)
        env.pop("SAILWRIGHT_ASKPASS_PASSWORD", None)
        env.pop("DEV_ALCHEMY_PROJECT_DIR", None)
        env.update(
            {
                "DEV_ALCHEMY_ROLE_SOURCES_CONFIG": str(FIXTURE / "ansible-role-sources.yml"),
                "DEV_ALCHEMY_APP_DATA_DIR": str(state / "app-data"),
                "DEV_ALCHEMY_CONFIG_DIR": str(state / "config"),
            }
        )
        env.setdefault("ANSIBLE_PYTHON_INTERPRETER", "auto_silent")
        env.setdefault("TERM", "xterm")
        cls.env = env

    @classmethod
    def tearDownClass(cls):
        temp = getattr(cls, "_temp", None)
        if temp is not None:
            temp.cleanup()

    def sailwright(self, *args, answer=None, max_prompts=1):
        run = run_in_pty([BIN, *args], self.env, self.workdir, answer=answer, max_prompts=max_prompts)
        print(f"\n----- sailwright {' '.join(args)} (exit {run.exit_code}, prompts {run.prompts}) -----")
        print(run.transcript.replace(PASSWORD, "***"))
        print("----- end -----", flush=True)
        if PASSWORD in run.transcript:
            self.fail("the become password appears in the output")
        return run

    def assert_no_timeout(self, run):
        if run.timed_out_reason is None:
            return
        hint = ""
        if PROMPT_ANYWHERE.search(run.plain):
            hint = (
                " The prompt text is in the output, but not at the start of a line "
                "waiting for input; it probably went through Sailwright's log "
                "prefixing instead of /dev/tty."
            )
        self.fail(f"{run.timed_out_reason}.{hint}")

    def provision(self, *extra, check=False, answer=PASSWORD, max_prompts=1):
        token = uuid.uuid4().hex
        args = ["provision", "local", "-K"]
        if check:
            args.append("--check")
        args += ["--", "-e", f"become_probe_token={token}"]
        if APT_PACKAGE:
            args += ["-e", f"become_probe_apt_package={APT_PACKAGE}"]
        args += list(extra)
        return token, self.sailwright(*args, answer=answer, max_prompts=max_prompts)

    def test_help_lists_ask_become_pass(self):
        run = self.sailwright("provision", "local", "--help")
        self.assertEqual(run.exit_code, 0)
        self.assertIn("-K, --ask-become-pass", run.plain)
        self.assertIn("sailwright provision local --ask-become-pass", run.plain)

    def test_check_mode_prompts_once(self):
        _, run = self.provision(check=True)
        self.assert_no_timeout(run)
        self.assertEqual(run.prompts, 1)
        self.assertEqual(run.exit_code, 0)
        self.assertRegex(run.plain, RECAP_OK)

    def test_prompts_once_and_runs_become_tasks(self):
        token, run = self.provision()
        self.assert_no_timeout(run)
        self.assertEqual(run.prompts, 1)
        self.assertEqual(run.exit_code, 0)
        self.assertRegex(run.plain, RECAP_OK)

        info = MARKER.stat()
        self.assertEqual(info.st_uid, 0, f"{MARKER} is not owned by root")
        self.assertEqual(MARKER.read_text().strip(), token, f"{MARKER} was not written by this run")
        if APT_PACKAGE:
            self.assertIsNotNone(shutil.which(APT_PACKAGE), f"{APT_PACKAGE} was not installed")

    def test_wrong_password_fails_without_asking_again(self):
        _, run = self.provision(check=True, answer=PASSWORD + "-wrong")
        self.assert_no_timeout(run)
        self.assertEqual(run.prompts, 1)
        self.assertNotEqual(run.exit_code, 0)
        self.assertRegex(run.plain, WRONG_PASSWORD)

    def test_rejects_become_password_file(self):
        for flag in ("--become-password-file", "--become-pass-file"):
            with self.subTest(flag=flag):
                run = self.sailwright(
                    "provision", "local", "-K", "--check", "--", flag, str(self.workdir / "unused"),
                    max_prompts=0,
                )
                self.assert_no_timeout(run)
                self.assertNotEqual(run.exit_code, 0)
                self.assertIn("--ask-become-pass cannot be combined with --become-password-file", run.plain)

    def test_rejects_vm_target(self):
        run = self.sailwright(
            "provision", "ubuntu", "--type", "server", "--arch", "amd64", "-K", "--check", max_prompts=0
        )
        self.assert_no_timeout(run)
        self.assertNotEqual(run.exit_code, 0)
        self.assertIn("--ask-become-pass is only supported for local provisioning", run.plain)

    def test_rejects_run_without_terminal(self):
        result = subprocess.run(
            [BIN, "provision", "local", "-K", "--check"],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            cwd=self.workdir,
            env=self.env,
            timeout=120,
            check=False,
        )
        output = strip_ansi(result.stdout + result.stderr)
        print(f"\n----- without terminal (exit {result.returncode}) -----\n{output}----- end -----")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--ask-become-pass needs an interactive terminal", output)
        self.assertIn("--become-password-file", output)


if __name__ == "__main__":
    unittest.main()
