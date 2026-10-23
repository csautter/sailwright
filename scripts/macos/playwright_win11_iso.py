"""
Fetches the latest Windows 11 ISO download link from Microsoft's official download page using Playwright.
"""

import argparse
import asyncio
import json
import os
import random
import sys
import time
from datetime import datetime, timezone
from urllib.parse import urlparse

from playwright.async_api import Error as PlaywrightError
from playwright.async_api import TimeoutError as PlaywrightTimeoutError
from playwright.async_api import async_playwright
from playwright_stealth import Stealth
from win11_iso_hash import extract_english_sha256

MICROSOFT_WIN11_ISO_URL = "https://www.microsoft.com/en-US/software-download/windows11"
MICROSOFT_WIN11_ARM_ISO_URL = (
    "https://www.microsoft.com/en-us/software-download/windows11arm64"
)

USER_AGENTS = [
    # Chrome on Windows
    # "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
    # Chrome on Mac
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
    # Chrome on Linux
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
    # Edge on Windows
    # "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.3719.115",
    # Edge on Mac
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.3719.115",
    # Firefox on Windows
    # "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:147.0) Gecko/20100101 Firefox/147.0",
    # Safari on Mac
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
]

DEFAULT_NAVIGATION_TIMEOUT_MS = 90000
DEFAULT_PAGE_READY_TIMEOUT_MS = 60000
DEFAULT_NAVIGATION_RETRIES = 3
# Must stay below the 10 minute process timeout in pkg/build/dependencies.go so the
# script fails on its own, with diagnostics, instead of being killed silently.
DEFAULT_LINK_TIMEOUT_SECONDS = 480
EXPECTED_PAGE_SELECTOR = "#product-edition"
# Shown by Microsoft instead of the download links when it refuses a request
# (e.g. from datacenter IP ranges). Retrying within the same session does not help.
MICROSOFT_REFUSAL_MARKERS = (
    "715-123130",
    "unable to complete your request",
)
# Exit code reported when Microsoft refuses or challenges the request, so callers can tell
# "blocked" (neutral) from "broken" (selectors changed, other errors).
EXIT_CODE_MICROSOFT_REFUSED = 3
CHALLENGE_MARKERS = (
    "verify you are human",
    "unusual traffic",
    "access denied",
    "temporarily unavailable",
    "captcha",
    "blocked",
    "bot",
    "challenge",
)


class MicrosoftRefusedError(RuntimeError):
    """Microsoft rejected or challenged the request (e.g. blocked datacenter IP)."""


def resolve_app_data_dir() -> str:
    override = os.environ.get("DEV_ALCHEMY_APP_DATA_DIR")
    if override:
        return os.path.abspath(override)

    if sys.platform == "darwin":
        return os.path.join(
            os.path.expanduser("~"),
            "Library",
            "Application Support",
            "dev-alchemy",
        )
    if os.name == "nt":
        base = os.environ.get("LOCALAPPDATA") or os.environ.get("APPDATA")
        if base:
            return os.path.join(base, "dev-alchemy")
        return os.path.join(
            os.path.expanduser("~"), "AppData", "Local", "dev-alchemy"
        )

    base = os.environ.get("XDG_DATA_HOME")
    if base:
        return os.path.join(base, "dev-alchemy")
    return os.path.join(os.path.expanduser("~"), ".local", "share", "dev-alchemy")


def resolve_cache_dir() -> str:
    override = os.environ.get("DEV_ALCHEMY_CACHE_DIR")
    if override:
        return os.path.abspath(override)
    return os.path.join(resolve_app_data_dir(), "cache")


def resolve_windows_cache_dir(*parts: str) -> str:
    return os.path.join(resolve_cache_dir(), "windows", *parts)


def resolve_cookie_path() -> str:
    return resolve_windows_cache_dir("playwright", "cookies.json")


def resolve_diagnostics_root() -> str:
    return resolve_windows_cache_dir("playwright-diagnostics")


def utc_timestamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def ensure_parent_dir(path: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)


def detect_challenge_markers(text: str) -> list[str]:
    lowered = text.lower()
    return [marker for marker in CHALLENGE_MARKERS if marker in lowered]


async def gather_page_state(page) -> dict:
    metadata = {
        "current_url": "",
        "title": "",
        "ready_state": "",
        "challenge_markers": [],
    }
    body_text = ""

    try:
        metadata["current_url"] = page.url
    except Exception:
        pass

    try:
        metadata["title"] = await page.title()
    except Exception:
        pass

    try:
        metadata["ready_state"] = await page.evaluate("() => document.readyState")
    except Exception:
        pass

    try:
        body_text = await page.locator("body").inner_text(timeout=5000)
    except Exception:
        pass

    combined_text = "\n".join(
        value
        for value in (metadata["title"], metadata["current_url"], body_text)
        if value
    )
    metadata["challenge_markers"] = detect_challenge_markers(combined_text)
    return metadata


async def write_failure_artifacts(page, diagnostics_dir: str, label: str) -> dict:
    os.makedirs(diagnostics_dir, exist_ok=True)

    metadata = await gather_page_state(page)
    metadata_path = os.path.join(diagnostics_dir, f"{label}.json")
    html_path = os.path.join(diagnostics_dir, f"{label}.html")
    screenshot_path = os.path.join(diagnostics_dir, f"{label}.png")

    try:
        ensure_parent_dir(metadata_path)
        with open(metadata_path, "w", encoding="utf-8") as f:
            json.dump(metadata, f, indent=2, sort_keys=True)
    except Exception as exc:
        print(f"[WARN] Failed to write metadata artifact {metadata_path}: {exc}")

    try:
        html = await page.content()
        ensure_parent_dir(html_path)
        with open(html_path, "w", encoding="utf-8") as f:
            f.write(html)
    except Exception as exc:
        print(f"[WARN] Failed to write HTML artifact {html_path}: {exc}")

    try:
        await page.screenshot(path=screenshot_path, full_page=True)
    except Exception as exc:
        print(f"[WARN] Failed to write screenshot artifact {screenshot_path}: {exc}")

    return metadata


async def random_mouse_movements(page, min_seconds=5, max_seconds=15):
    duration = random.uniform(min_seconds, max_seconds)
    start_time = time.time()
    box = await page.evaluate(
        """() => {
            const { width, height } = document.body.getBoundingClientRect();
            return { width, height };
        }"""
    )
    width = box["width"]
    height = box["height"]
    while time.time() - start_time < duration:
        x = random.randint(0, int(width) - 1)
        y = random.randint(0, int(height) - 1)
        await page.mouse.move(x, y, steps=random.randint(5, 20))
        # Randomly scroll sometimes
        if random.random() < 0.3:
            scroll_amount = random.randint(-100, 100)
            await page.mouse.wheel(0, scroll_amount)
        await asyncio.sleep(random.uniform(0.1, 0.5))


async def dismiss_modal_if_present(page):
    try:
        selector = 'button:has-text("Close")'
        await page.wait_for_selector(selector, timeout=120000)
        modal_dismiss = page.locator(selector)
        if modal_dismiss and await modal_dismiss.is_visible(timeout=30000):
            print("Dismissing modal...")
            await page.click(selector)
    except Exception:
        pass


async def raise_if_microsoft_refused(page):
    try:
        body_text = (await page.locator("body").inner_text(timeout=5000)).lower()
    except Exception:
        return
    markers = [marker for marker in MICROSOFT_REFUSAL_MARKERS if marker in body_text]
    if markers:
        raise MicrosoftRefusedError(
            "Microsoft refused the Windows 11 download request "
            f"(detected: {', '.join(markers)}). This usually means the client IP is blocked."
        )


async def click_button_with_retry(page, selector, selector_condition, retries=10):
    last_error = None
    for attempt in range(1, retries + 1):
        try:
            await page.wait_for_selector(selector, timeout=60000)
            button = page.locator(selector)
            if button and await button.is_visible(timeout=30000):
                await page.click(selector)

            else:
                print(f"{selector} button is not visible.")
        except Exception as exc:
            last_error = exc
            print(
                f"[WARN] Attempt {attempt}/{retries} failed while clicking {selector}: {exc}"
            )

        try:
            await page.wait_for_selector(selector_condition, timeout=30000)
        except Exception as exc:
            last_error = exc

        expected = page.locator(selector_condition)
        if expected and await expected.is_visible(timeout=30000):
            return True
        else:
            await raise_if_microsoft_refused(page)
            await dismiss_modal_if_present(page)

    raise RuntimeError(
        f"Failed to advance from {selector} to {selector_condition} after {retries} attempts."
    ) from last_error


async def select_option_by_text(page, selector, text_match):
    options = await page.query_selector_all(f"{selector} option")
    for option in options:
        text = await option.text_content()
        value = await option.get_attribute("value")
        if text and text_match in text:
            await page.select_option(selector, value=value)
            return value
    return None


async def navigate_to_download_page(
    page,
    url: str,
    diagnostics_dir: str,
    navigation_timeout_ms: int,
    page_ready_timeout_ms: int,
    retries: int,
):
    last_error = None
    last_metadata = None

    for attempt in range(1, retries + 1):
        print(
            f"[INFO] Navigating to {url} (attempt {attempt}/{retries}, timeout={navigation_timeout_ms}ms)"
        )
        try:
            response = await page.goto(
                url,
                timeout=navigation_timeout_ms,
                wait_until="domcontentloaded",
            )
            if response is not None:
                print(
                    f"[INFO] Navigation response: status={response.status} url={response.url}"
                )

            await page.wait_for_selector(
                EXPECTED_PAGE_SELECTOR, timeout=page_ready_timeout_ms
            )
            return
        except (PlaywrightTimeoutError, PlaywrightError) as exc:
            last_error = exc
            print(f"[WARN] Navigation attempt {attempt}/{retries} failed: {exc}")
            last_metadata = await write_failure_artifacts(
                page, diagnostics_dir, f"initial-navigation-attempt-{attempt}"
            )
            if last_metadata.get("challenge_markers"):
                print(
                    "[WARN] Page looks like a challenge/blocked response. "
                    f"Detected markers: {', '.join(last_metadata['challenge_markers'])}"
                )

            if attempt < retries:
                sleep_seconds = min(20.0, (2**attempt) + random.uniform(0.5, 1.5))
                print(f"[INFO] Retrying navigation after {sleep_seconds:.1f}s")
                await asyncio.sleep(sleep_seconds)

    challenge_hint = ""
    if last_metadata and last_metadata.get("challenge_markers"):
        challenge_hint = (
            " Possible anti-bot or blocked-page markers were detected: "
            + ", ".join(last_metadata["challenge_markers"])
            + "."
        )

    message = (
        "Could not load the Microsoft Windows 11 download page after "
        f"{retries} attempts. Diagnostics were written to {diagnostics_dir}.{challenge_hint}"
    )
    if last_metadata and last_metadata.get("challenge_markers"):
        raise MicrosoftRefusedError(message) from last_error
    raise RuntimeError(message) from last_error


async def find_english_sha256(page) -> str:
    """Best effort: read the sha256 of the English (United States) ISO from the hash table."""
    # Table rows first: one row per language keeps the label and its hash together. Then the
    # whole page text; text_content() includes collapsed sections that inner_text() skips.
    try:
        rows = await page.locator("tr").all_text_contents()
    except Exception:
        rows = []
    for row in rows:
        sha256 = extract_english_sha256(row)
        if sha256:
            return sha256
    try:
        text = await page.locator("body").text_content(timeout=5000) or ""
    except Exception as exc:
        print(f"[WARN] Could not read the page text for the sha256 lookup: {exc}")
        return ""
    sha256 = extract_english_sha256(text)
    if not sha256:
        print(
            "[WARN] No sha256 for the English (United States) ISO found on the download page; "
            "the ISO cannot be verified against Microsoft's published hash."
        )
    return sha256


async def write_link_info(page, link: str, arm: bool, output_dir: str) -> None:
    source_name = os.path.basename(urlparse(link).path)
    info = {
        "url": link,
        "source_name": source_name,
        "sha256": await find_english_sha256(page),
    }
    info_path = os.path.join(
        output_dir,
        "win11_arm64_iso_link.json" if arm else "win11_amd64_iso_link.json",
    )
    with open(info_path, "w", encoding="utf-8") as f:
        json.dump(info, f, indent=2)


async def resolve_download_link(page, url: str, arm: bool, diagnostics_dir: str) -> str:
    await navigate_to_download_page(
        page=page,
        url=url,
        diagnostics_dir=diagnostics_dir,
        navigation_timeout_ms=DEFAULT_NAVIGATION_TIMEOUT_MS,
        page_ready_timeout_ms=DEFAULT_PAGE_READY_TIMEOUT_MS,
        retries=DEFAULT_NAVIGATION_RETRIES,
    )

    await random_mouse_movements(page)

    # Accept cookies if prompted
    try:
        await page.click('button:has-text("Accept")', timeout=3000)
    except Exception:
        pass

    await random_mouse_movements(page)

    # Select 'Windows 11 (multi-edition ISO)' from the dropdown
    selected_product = await select_option_by_text(
        page, "#product-edition", "Windows 11"
    )
    if selected_product is None:
        raise RuntimeError(
            "Could not find a Windows 11 product option on the Microsoft download page."
        )

    expect_creation_of_selector = "#product-languages"
    await click_button_with_retry(
        page, "#submit-product-edition", expect_creation_of_selector
    )

    await random_mouse_movements(page)

    # Select English (United States)
    lang_value = await select_option_by_text(
        page, "#product-languages", "English (United States)"
    )
    if lang_value is None:
        raise RuntimeError("Could not find 'English (United States)' language option.")

    await random_mouse_movements(page)

    # expected selector
    download_selector = "#download-links > div > div > a:first-child"
    await click_button_with_retry(page, "#submit-sku", download_selector)

    await random_mouse_movements(page)

    await page.wait_for_selector(download_selector, timeout=60000)
    download_button = page.locator(download_selector)
    link = await download_button.get_attribute("href")
    if not link:
        raise RuntimeError("Could not retrieve the download link.")

    print(f"Windows 11 ISO download link: {link}")

    # Write link to file for use in packer build
    output_dir = resolve_windows_cache_dir()
    os.makedirs(output_dir, exist_ok=True)
    output_path = os.path.join(
        output_dir,
        "win11_arm64_iso_url.txt" if arm else "win11_amd64_iso_url.txt",
    )
    with open(output_path, "w", encoding="utf-8") as f:
        f.write(link)

    await write_link_info(page, link, arm, output_dir)

    return link


async def fetch_win11_iso_link(
    arm: bool = False,
    headless: bool = False,
    download: bool = False,
    save_path: str = "",
    link_timeout_seconds: int = DEFAULT_LINK_TIMEOUT_SECONDS,
):
    async with Stealth().use_async(async_playwright()) as p:
        browser = await p.chromium.launch(
            headless=headless,
            args=["--disable-blink-features=AutomationControlled"],
        )

        # Randomly select viewport size
        width = random.randint(800, 1920)
        height = random.randint(600, 1080)
        print(f"[INFO] Using viewport size: {width}x{height}")

        user_agent = random.choice(USER_AGENTS)

        context = await browser.new_context(
            locale="en-US",
            timezone_id="America/New_York",
            user_agent=user_agent,
            viewport={"width": width, "height": height},
        )
        context.set_default_timeout(DEFAULT_PAGE_READY_TIMEOUT_MS)
        context.set_default_navigation_timeout(DEFAULT_NAVIGATION_TIMEOUT_MS)
        # Load cookies from a previous session
        cookie_path = resolve_cookie_path()
        try:
            with open(cookie_path, "r", encoding="utf-8") as f:
                saved_cookies = json.load(f)
            await context.add_cookies(saved_cookies)
        except (FileNotFoundError, json.JSONDecodeError):
            pass

        # Inject JavaScript to disable the webdriver flag
        await context.add_init_script(
            """
            Object.defineProperty(navigator, 'webdriver', {
            get: () => undefined
            })
            """
        )
        page = await context.new_page()

        url = MICROSOFT_WIN11_ARM_ISO_URL if arm else MICROSOFT_WIN11_ISO_URL
        diagnostics_dir = os.path.join(
            resolve_diagnostics_root(),
            f"{'arm64' if arm else 'amd64'}-{utc_timestamp()}",
        )

        try:
            try:
                link = await asyncio.wait_for(
                    resolve_download_link(page, url, arm, diagnostics_dir),
                    timeout=link_timeout_seconds,
                )
            except asyncio.TimeoutError:
                raise RuntimeError(
                    f"Timed out after {link_timeout_seconds}s while resolving the Windows 11 "
                    f"download link. Diagnostics were written to {diagnostics_dir}."
                ) from None

            if download:
                print("Starting ISO download...")
                async with page.expect_download() as download_info:
                    await page.goto(link, wait_until="commit")
                download_obj = await download_info.value
                await download_obj.save_as(save_path)
                print(f"ISO saved to {save_path}")

            # Save current session cookies for future use
            current_cookies = await context.cookies()
            ensure_parent_dir(cookie_path)
            with open(cookie_path, "w", encoding="utf-8") as f:
                json.dump(current_cookies, f)
        except Exception:
            await write_failure_artifacts(page, diagnostics_dir, "fatal-error")
            raise
        finally:
            await browser.close()


if __name__ == "__main__":
    # Stdout is a pipe when run from Go; flush per line so progress survives a kill.
    sys.stdout.reconfigure(line_buffering=True)
    sys.stderr.reconfigure(line_buffering=True)

    parser = argparse.ArgumentParser(
        description="Fetch latest Windows 11 ISO download link."
    )
    parser.add_argument(
        "--arm", action="store_true", help="Fetch Windows 11 ARM version ISO"
    )
    parser.add_argument(
        "--headless",
        type=lambda x: (str(x).lower() == "true"),
        default=True,
        help="Run browser in headless mode (true/false, default: true)",
    )
    parser.add_argument(
        "--download",
        action="store_true",
        help="Automatically download the ISO after fetching the link",
    )
    parser.add_argument(
        "--save-path",
        type=str,
        default="windows11.iso",
        help="Path to save the downloaded ISO (default: windows11.iso)",
    )
    parser.add_argument(
        "--link-timeout",
        type=int,
        default=DEFAULT_LINK_TIMEOUT_SECONDS,
        help="Seconds allowed for resolving the download link "
        f"(default: {DEFAULT_LINK_TIMEOUT_SECONDS})",
    )
    args = parser.parse_args()
    try:
        asyncio.run(
            fetch_win11_iso_link(
                arm=args.arm,
                headless=args.headless,
                download=args.download,
                save_path=args.save_path,
                link_timeout_seconds=args.link_timeout,
            )
        )
    except MicrosoftRefusedError as exc:
        print(f"[REFUSED] {exc}", file=sys.stderr)
        sys.exit(EXIT_CODE_MICROSOFT_REFUSED)
