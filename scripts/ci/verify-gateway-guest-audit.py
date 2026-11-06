#!/usr/bin/env python3
"""Require real bidirectional guest traffic and completed gateway tunnel audits."""
import collections
import json
import sys


def verify(events):
    if not any(e["event"] == "vm_info" for e in events):
        raise ValueError("missing vm_info")
    opened = collections.Counter()
    closed = collections.Counter()
    traffic = False
    for event in events:
        if event["event"] not in ("tunnel_opened", "tunnel_closed"):
            continue
        params = event["params"]
        key = tuple(params.get(k) for k in ("os", "type", "arch", "address", "port"))
        if event["event"] == "tunnel_opened":
            opened[key] += 1
        else:
            closed[key] += 1
            traffic |= params.get("bytes_to_vm", 0) > 0 and params.get("bytes_from_vm", 0) > 0
    if not opened or opened != closed:
        raise ValueError("missing or unpaired tunnel events")
    if not traffic:
        raise ValueError("no bidirectional guest traffic")


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as audit:
        verify([json.loads(line) for line in audit if line.strip()])
    print("PASS: vm_info, paired tunnel events and bidirectional guest traffic")
