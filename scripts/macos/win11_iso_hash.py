"""
Parses the sha256 of the English (United States) ISO from the text of Microsoft's Windows 11
download page ("Verify your download" table). Kept free of Playwright imports so it can be unit
tested on its own (test/scripts/test_win11_iso_hash.py).
"""

import re

ENGLISH_US_LABEL = "English (United States)"
# A row is "<language> <hash>"; allow table/cell separators and other labels in between, but stop
# before the next row's language could be reached.
_MAX_LABEL_TO_HASH_CHARS = 200
_SHA256 = re.compile(r"(?<![0-9A-Fa-f])[0-9A-Fa-f]{64}(?![0-9A-Fa-f])")


def extract_english_sha256(text: str) -> str:
    """Returns the lower-case sha256 following the first "English (United States)" label, or ""."""
    start = 0
    while True:
        index = text.find(ENGLISH_US_LABEL, start)
        if index < 0:
            return ""
        window_start = index + len(ENGLISH_US_LABEL)
        match = _SHA256.search(text, window_start, window_start + _MAX_LABEL_TO_HASH_CHARS)
        if match:
            return match.group(0).lower()
        start = window_start
