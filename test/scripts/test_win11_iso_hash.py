"""Unit tests for scripts/macos/win11_iso_hash.py. Run: python3 -m unittest discover -s test/scripts"""

import os
import sys
import unittest

sys.path.insert(
    0, os.path.join(os.path.dirname(__file__), "..", "..", "scripts", "macos")
)

from win11_iso_hash import extract_english_sha256  # noqa: E402

US = "a" * 63 + "1"
INTL = "b" * 64


class ExtractEnglishSha256Test(unittest.TestCase):
    def test_table_row(self):
        self.assertEqual(extract_english_sha256(f"English (United States)\t{US}"), US)

    def test_upper_case_is_normalized(self):
        self.assertEqual(
            extract_english_sha256(f"English (United States) {US.upper()}"), US
        )

    def test_hex_characters_between_label_and_hash(self):
        # The previous regex stopped at the first hex digit ("64-bit", "Edition").
        text = f"English (United States) | 64-bit Edition | {US}"
        self.assertEqual(extract_english_sha256(text), US)

    def test_picks_the_us_row_among_languages(self):
        text = f"Language Hash\nEnglish International {INTL}\nEnglish (United States) {US}\n"
        self.assertEqual(extract_english_sha256(text), US)

    def test_label_without_hash_then_table(self):
        # The language dropdown mentions the label long before the hash table.
        text = (
            "Select language: English (United States)"
            + " " * 300
            + f"Verify your download\nEnglish (United States) {US}"
        )
        self.assertEqual(extract_english_sha256(text), US)

    def test_does_not_take_a_hash_from_far_away(self):
        text = "English (United States)" + " " * 300 + US
        self.assertEqual(extract_english_sha256(text), "")

    def test_longer_hex_runs_are_not_hashes(self):
        self.assertEqual(
            extract_english_sha256(f"English (United States) {US}ff"), ""
        )

    def test_missing(self):
        self.assertEqual(extract_english_sha256("no table here"), "")


if __name__ == "__main__":
    unittest.main()
