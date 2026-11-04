#!/usr/bin/env python3
"""Capture Packer's original QEMU argv without starting a guest."""
import json
import os
from pathlib import Path
import sys
import time

if any(arg in sys.argv for arg in ('--version', '-version')):
    print('QEMU emulator version 11.1.2')
else:
    # Atomic publication lets the parent stop Packer only after the full capture.
    destination = Path(os.environ['MACOS_PROFILE_ARGV'])
    temporary = destination.with_suffix('.tmp')
    temporary.write_text(json.dumps(sys.argv[1:]))
    temporary.replace(destination)
    while True:
        time.sleep(1)
