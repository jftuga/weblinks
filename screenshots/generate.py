# /// script
# requires-python = ">=3.14"
# dependencies = ["playwright==1.63.0"]
# ///
"""Regenerates the README screenshots in this directory.

Builds the weblinks binary from the current source, serves a throwaway demo
store on a spare port, and captures the main page and the bookmarklet's add
popup in dark mode with headless Chromium. The real weblinks.json is never read
or written.

Usage, from anywhere in the repository:

    uv run screenshots/generate.py

If Chromium is missing, install it once with:

    uv run --with playwright==1.63.0 playwright install chromium

Layout constraints that shaped the demo data:

- main.png is 1024x768, which holds three sections of four links. A fourth
  section, or a fifth link in any section, pushes content off the bottom.
- Notes are shown on the last section only. A note doubles the height of its
  row, so notes on an earlier section push the sections below it off-screen.
- Every link but two has been clicked, filling the recently-clicked column to
  roughly the height of the sections column. Clicked links sort to the top of
  their section, so changing a click time can reorder a section.
- Click and check times are offsets from the moment of capture, so the
  "minutes ago" and "days ago" labels come out the same on every run.
- add-weblink.png is 640x680, the size of the window the bookmarklet opens.
"""

import argparse
import json
import subprocess
import tempfile
import time
import urllib.request
from datetime import UTC, datetime, timedelta
from pathlib import Path
from urllib.parse import urlencode

from playwright.sync_api import Browser, Page, sync_playwright

ADDR = "127.0.0.1:8765"
BASE = f"http://{ADDR}"
REPO = Path(__file__).resolve().parent.parent
MINUTE = 1
HOUR = 60 * MINUTE
DAY = 24 * HOUR

# Each entry is (title, url, notes, minutes since last click or None,
# check result or None). A check result is (category, status, detail), using
# the categories and detail phrases the link checker itself produces.
SECTIONS: dict[str, list[tuple[str, str, str, int | None, tuple[str, int, str] | None]]] = {
    "Computing": [
        ("Go Documentation", "https://go.dev/doc/", "Tutorials, effective Go and the language spec", 4 * MINUTE, None),
        ("Hacker News", "https://news.ycombinator.com/", "", 35 * MINUTE, None),
        ("Stack Overflow", "https://stackoverflow.com/", "", None, ("auth", 403, "403 Forbidden")),
        ("ArchWiki", "https://wiki.archlinux.org/", "Useful well beyond Arch", 30 * HOUR, None),
    ],
    "Music": [
        ("Ultimate Guitar Tabs", "https://www.ultimate-guitar.com/", "", 50 * HOUR, None),
        ("IMSLP Sheet Music Library", "https://imslp.org/", "Public domain scores", 2 * HOUR, None),
        ("musictheory.net", "https://www.musictheory.net/", "", 7 * DAY, None),
        ("Pitchfork Album Reviews", "https://pitchfork.com/reviews/albums/", "", None, ("unknown", 0, "timed out")),
    ],
    "Travel": [
        ("Wikivoyage", "https://www.wikivoyage.org/", "Free, community-written travel guides", 12 * MINUTE, None),
        ("Google Flights", "https://www.google.com/travel/flights", "Use the date grid to find the cheapest days", 5 * HOUR, None),
        ("Rome2Rio", "https://www.rome2rio.com/", "Compares train, bus, ferry and flight routes", 3 * DAY, None),
        ("National Park Service", "https://www.nps.gov/", "Alerts, closures and park passes", 5 * DAY, None),
    ],
}

NOTES_SECTION = "Travel"

# What the bookmarklet hands to the add popup, and what is typed into it.
ADD_URL = "https://en.wikivoyage.org/wiki/Kyoto"
ADD_TITLE = "Kyoto - Travel guide at Wikivoyage"
ADD_SECTION = "Travel"
ADD_NOTES = "Temples, day trips and the rail pass"


def iso(moment: datetime) -> str:
    """Formats a datetime as an RFC 3339 UTC timestamp.

    Args:
        moment: The time to format.

    Returns:
        The timestamp string with a trailing Z.
    """
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def build_store(now: datetime) -> dict[str, object]:
    """Builds the demo weblinks document from SECTIONS.

    Args:
        now: The reference time for click and check timestamps.

    Returns:
        A document in the weblinks.json schema.
    """
    sections = []
    counter = 0
    for name, links in SECTIONS.items():
        entries = []
        for title, url, notes, clicked, check in links:
            counter += 1
            category, status, detail = check or ("", 0, "")
            entries.append({
                "id": f"{counter:012x}",
                "url": url,
                "title": title,
                "notes": notes,
                "created": iso(now - timedelta(days=60)),
                "last_clicked": iso(now - timedelta(minutes=clicked)) if clicked is not None else None,
                "click_count": 3 if clicked is not None else 0,
                "skip_check": False,
                "last_checked": iso(now - timedelta(hours=2)) if check else None,
                "last_status": status,
                "last_result": category,
                "last_detail": detail,
            })
        sections.append({"name": name, "links": entries})
    return {"title": "Weblinks", "sections": sections}


def build_binary(dest: Path) -> None:
    """Compiles weblinks from the current source.

    Args:
        dest: Path to write the binary to.

    Raises:
        subprocess.CalledProcessError: The build failed.
    """
    subprocess.run(["go", "build", "-o", str(dest), "."], cwd=REPO, check=True)


def wait_for_server(timeout: float) -> None:
    """Polls the server until it answers or the timeout expires.

    Args:
        timeout: Seconds to wait before giving up.

    Raises:
        RuntimeError: The server did not respond in time.
    """
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/", timeout=1):
                return
        except OSError:
            time.sleep(0.1)
    raise RuntimeError(f"server at {BASE} did not start")


def new_page(browser: Browser, width: int, height: int) -> Page:
    """Opens a dark-mode page with the theme and notes preferences preset.

    The preferences are written to localStorage by an init script, so they are
    in place before the app's own script reads them.

    Args:
        browser: The browser to open the page in.
        width: Viewport width in CSS pixels.
        height: Viewport height in CSS pixels.

    Returns:
        The new page.
    """
    context = browser.new_context(viewport={"width": width, "height": height}, color_scheme="dark")
    page = context.new_page()
    page.add_init_script(
        "localStorage.setItem('weblinks.theme', JSON.stringify('dark'));"
        f"localStorage.setItem('weblinks.notes', JSON.stringify([{json.dumps(NOTES_SECTION)}]));"
    )
    return page


def shoot_main(page: Page, out: Path) -> None:
    """Captures the main page once the recently-clicked panel has rendered.

    Args:
        page: A page sized 1024x768.
        out: Destination PNG path.
    """
    page.goto(BASE + "/")
    page.wait_for_selector("#recent-list li")
    page.mouse.move(0, 0)
    page.screenshot(path=str(out), animations="disabled")


def shoot_add(page: Page, out: Path) -> None:
    """Captures the add dialog as the bookmarklet popup opens it.

    Args:
        page: A page sized like the bookmarklet popup.
        out: Destination PNG path.
    """
    query = urlencode({"url": ADD_URL, "title": ADD_TITLE})
    page.goto(f"{BASE}/add?{query}")
    page.wait_for_selector("#link-dialog[open]")
    page.select_option("#f-section", ADD_SECTION)
    page.fill("#f-notes", ADD_NOTES)
    page.locator("#f-notes").blur()
    page.screenshot(path=str(out), animations="disabled")


def main() -> None:
    """Parses arguments, serves the demo store and writes both screenshots."""
    parser = argparse.ArgumentParser(description="Regenerate the README screenshots.")
    parser.add_argument("--outdir", type=Path, default=Path(__file__).resolve().parent, help="directory for the PNG files (default: this script's directory)")
    args = parser.parse_args()

    with tempfile.TemporaryDirectory() as tmp:
        binary = Path(tmp) / "weblinks"
        store = Path(tmp) / "weblinks.json"
        build_binary(binary)
        store.write_text(json.dumps(build_store(datetime.now(UTC)), indent=2))

        server = subprocess.Popen([str(binary), "-addr", ADDR, "-file", str(store), "-no-open"])
        try:
            wait_for_server(10)
            with sync_playwright() as pw:
                browser = pw.chromium.launch()
                shoot_main(new_page(browser, 1024, 768), args.outdir / "main.png")
                shoot_add(new_page(browser, 640, 680), args.outdir / "add-weblink.png")
                browser.close()
        finally:
            server.terminate()
            server.wait()


if __name__ == "__main__":
    main()
