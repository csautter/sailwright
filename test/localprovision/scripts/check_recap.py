"""Validate Ansible recaps in Sailwright's prefixed, optionally colored logs."""

import re
import sys
from pathlib import Path

ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
RECAP = re.compile(r"\bok=\d+\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)\b")


def validate(output, phase):
    output = ANSI.sub("", output)
    if "PLAY RECAP" not in output:
        raise ValueError("No PLAY RECAP found")
    recap = output.rsplit("PLAY RECAP", 1)[1]
    rows = RECAP.findall(recap)
    if not rows:
        raise ValueError("No host recap found")
    if any(int(unreachable) or int(failed) for _, unreachable, failed in rows):
        raise ValueError("Failed or unreachable hosts:\n" + recap)
    if phase == "idempotency" and any(int(changed) for changed, _, _ in rows):
        task = "(unknown task)"
        changes = []
        for line in output.splitlines():
            if "TASK [" in line or "RUNNING HANDLER [" in line:
                task = line
            if re.search(r"\bchanged: \[", line):
                changes.append(task + "\n" + line)
        raise ValueError("Second run changed tasks:\n" + "\n".join(changes) + "\n" + recap)
    return recap.strip()


if __name__ == "__main__":
    try:
        print(validate(Path(sys.argv[1]).read_text(), sys.argv[2]))
    except ValueError as error:
        sys.exit(str(error))
