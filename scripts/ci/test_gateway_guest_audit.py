"""Negative controls for the evidence check used by the real guest workflow."""
import copy
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location(
    "audit_check", pathlib.Path(__file__).with_name("verify-gateway-guest-audit.py")
)
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class GuestAuditTest(unittest.TestCase):
    def setUp(self):
        params = {"os": "ubuntu", "type": "server", "arch": "amd64", "address": "192.168.122.2", "port": 22}
        self.events = [
            {"event": "vm_info"},
            {"event": "tunnel_opened", "params": params.copy()},
            {"event": "tunnel_closed", "params": {**params, "bytes_to_vm": 10, "bytes_from_vm": 20}},
        ]

    def test_real_traffic(self):
        check.verify(self.events)

    def test_build_only_is_insufficient(self):
        with self.assertRaisesRegex(ValueError, "vm_info"):
            check.verify([{"event": "job_finished", "tool": "vm_build"}])

    def test_unclosed_tunnel(self):
        with self.assertRaisesRegex(ValueError, "unpaired"):
            check.verify(self.events[:-1])

    def test_wrong_guest_or_port_is_not_paired(self):
        for field, value in (("address", "192.168.122.3"), ("port", 5985)):
            events = copy.deepcopy(self.events)
            events[-1]["params"][field] = value
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "unpaired"):
                check.verify(events)

    def test_one_way_traffic_is_insufficient(self):
        for field in ("bytes_to_vm", "bytes_from_vm"):
            events = copy.deepcopy(self.events)
            events[-1]["params"][field] = 0
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "bidirectional"):
                check.verify(events)


if __name__ == "__main__":
    unittest.main()
