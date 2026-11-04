#!/usr/bin/env python3
"""Load production shell functions without executing installers/builds."""
from pathlib import Path
import re
import sys
import subprocess

for name in sys.argv[1:]:
    text = Path(name).read_text()
    starts = list(re.finditer(r"(?m)^[a-zA-Z_]\w*\(\) \{", text))
    for index, start in enumerate(starts):
        end = starts[index + 1].start() if index + 1 < len(starts) else len(text)
        section = text[start.start():end]
        # Ask Bash to distinguish shell braces from quoted awk program braces.
        for close in re.finditer(r"(?m)^}[ \t]*$", section):
            candidate = section[:close.end()]
            result = subprocess.run(['bash', '-n'], input=candidate, text=True,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if result.returncode == 0:
                print(candidate)
                break
        else:
            raise ValueError(f'Cannot extract function from {name}: {start.group()}')
