#!/usr/bin/env python3
"""Drive a command in a pseudo-terminal the way a user at the keyboard would.

The command becomes the foreground process group of a new terminal, so writing
Ctrl+C (0x03) to the terminal makes the kernel deliver SIGINT to it, exactly as a
real keypress does. Steps run in order:

  wait:REGEX[:SECONDS]   wait until the output matches REGEX (default 600 s)
  sleep:SECONDS          wait a fixed time
  intr                   press Ctrl+C
  exit[:SECONDS]         wait for the command to exit (default 120 s)

The transcript (ANSI sequences removed) goes to --log and stderr, so callers can
assert on command output while parsing the JSON summary printed
on stdout: the exit status, seconds from the last Ctrl+C to exit, and per-step
timings. Exit status: the command's own status, 124 if a step timed out (the
command is then killed), 125 for usage errors.

Example:
  ptyctl.py --log build.log --step 'wait:Downloading' --step intr \
      --step exit:30 -- sailwright build ubuntu --arch arm64 --headless
"""

import argparse
import json
import os
import pty
import re
import select
import signal
import sys
import time

ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\r")


class Session:
    def __init__(self, argv, log_path):
        self.log = open(log_path, "w", encoding="utf-8")
        self.buffer = ""
        self.status = None
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            try:
                os.execvp(argv[0], argv)
            except OSError as error:
                print(f"ptyctl: cannot run {argv[0]}: {error}", file=sys.stderr)
                os._exit(127)

    def pump(self, timeout):
        """Read available output for up to timeout seconds."""
        if self.fd is None:
            time.sleep(timeout)
            return
        ready, _, _ = select.select([self.fd], [], [], timeout)
        if not ready:
            self.reap()
            return
        try:
            data = os.read(self.fd, 65536)
        except OSError:
            data = b""
        if not data:
            os.close(self.fd)
            self.fd = None
            self.reap(block=True)
            return
        text = ANSI.sub("", data.decode("utf-8", errors="replace"))
        self.log.write(text)
        self.log.flush()
        sys.stderr.write(text)
        sys.stderr.flush()
        self.buffer = (self.buffer + text)[-1_000_000:]

    def reap(self, block=False):
        if self.status is not None:
            return
        try:
            pid, status = os.waitpid(self.pid, 0 if block else os.WNOHANG)
        except ChildProcessError:
            self.status = -1
            return
        if pid:
            if os.WIFEXITED(status):
                self.status = os.WEXITSTATUS(status)
            elif os.WIFSIGNALED(status):
                self.status = 128 + os.WTERMSIG(status)
            else:
                self.status = -1

    def running(self):
        self.reap()
        return self.status is None

    def kill(self):
        if self.running():
            try:
                os.killpg(os.getpgid(self.pid), signal.SIGKILL)
            except (ProcessLookupError, PermissionError):
                pass
            self.reap(block=True)


def parse_step(text):
    kind, _, rest = text.partition(":")
    if kind == "wait":
        pattern, sep, seconds = rest.rpartition(":")
        if not sep or not re.fullmatch(r"\d+(\.\d+)?", seconds):
            pattern, seconds = rest, "600"
        return ("wait", re.compile(pattern), float(seconds))
    if kind == "sleep":
        return ("sleep", None, float(rest))
    if kind == "intr" and not rest:
        return ("intr", None, 0.0)
    if kind == "exit":
        return ("exit", None, float(rest or 120))
    raise ValueError(f"unknown step {text!r}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--log", required=True)
    parser.add_argument("--step", action="append", default=[])
    parser.add_argument("argv", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    argv = args.argv[1:] if args.argv[:1] == ["--"] else args.argv
    if not argv:
        parser.error("missing command")
    try:
        steps = [parse_step(step) for step in args.step]
    except (ValueError, re.error) as error:
        parser.error(str(error))

    session = Session(argv, args.log)
    started = time.monotonic()
    last_intr = None
    report = {"argv": argv, "steps": []}
    code = None
    for kind, pattern, seconds in steps:
        step_started = time.monotonic()
        entry = {"step": kind if pattern is None else f"wait:{pattern.pattern}"}
        if kind == "wait":
            deadline = step_started + seconds
            while not pattern.search(session.buffer):
                if time.monotonic() > deadline or not session.running() and session.fd is None:
                    entry["result"] = "timeout" if session.running() else "exited before match"
                    code = 124
                    break
                session.pump(0.2)
        elif kind == "sleep":
            deadline = step_started + seconds
            while time.monotonic() < deadline:
                session.pump(min(0.2, max(0.0, deadline - time.monotonic())))
        elif kind == "intr":
            if session.fd is not None:
                os.write(session.fd, b"\x03")
            last_intr = time.monotonic()
        elif kind == "exit":
            deadline = step_started + seconds
            while session.running() or session.fd is not None:
                if time.monotonic() > deadline:
                    entry["result"] = "still running"
                    code = 124
                    break
                session.pump(0.2)
        entry["seconds"] = round(time.monotonic() - step_started, 2)
        report["steps"].append(entry)
        if code is not None:
            break

    if code is None:
        while session.running() or session.fd is not None:
            session.pump(0.2)
            if time.monotonic() - started > 6 * 3600:
                code = 124
                break
    if code == 124:
        session.kill()
    else:
        code = session.status
    report["exit"] = session.status
    report["seconds"] = round(time.monotonic() - started, 2)
    if last_intr is not None:
        report["seconds_after_last_intr"] = round(time.monotonic() - last_intr, 2)
    print(json.dumps(report))
    session.log.close()
    return code if code is not None else 0


if __name__ == "__main__":
    sys.exit(main())
