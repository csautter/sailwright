"""
Parses the sha256 of the English (United States) ISO from the text of Microsoft's Windows 11
download page ("Verify your download" table). Kept free of Playwright imports so it can be unit
tested on its own (test/scripts/test_win11_iso_hash.py).
"""

import re

ENGLISH_US_LABEL = "English (United States)"
# Text-only parsing is limited to a single line. The browser adapter below preserves table
# boundaries and normalizes whitespace within each cell before passing a row here.
_MAX_LABEL_TO_HASH_CHARS = 200
_SHA256 = re.compile(r"(?<![0-9A-Fa-f])[0-9A-Fa-f]{64}(?![0-9A-Fa-f])")


def extract_english_sha256(text: str) -> str:
    """Return the digest on the same text row as the US label, or an empty string."""
    for line in text.splitlines():
        index = line.find(ENGLISH_US_LABEL)
        if index < 0:
            continue
        start = index + len(ENGLISH_US_LABEL)
        match = _SHA256.search(line, start, start + _MAX_LABEL_TO_HASH_CHARS)
        if match:
            return match.group(0).lower()
    return ""


async def find_english_sha256(page) -> str:
    """Read only structurally identified US table rows, including collapsed sections."""
    try:
        rows = await page.locator("tr").evaluate_all(
            r"""rows => rows.map(row => Array.from(row.cells, cell =>
                (cell.textContent || '').replace(/\s+/g, ' ').trim()).join('\t'))"""
        )
    except Exception as exc:
        print(f"[WARN] Could not read the hash table for the sha256 lookup: {exc}")
        rows = []
    for row in rows:
        cells = row.split("\t")
        if cells and cells[0] == ENGLISH_US_LABEL:
            sha256 = extract_english_sha256(row)
            if sha256:
                return sha256
    print(
        "[WARN] No sha256 for the English (United States) ISO found on the download page; "
        "the ISO cannot be verified against Microsoft's published hash."
    )
    return ""
