import logging
import re
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

import requests
from bs4 import BeautifulSoup
from sqlmodel import Session, select

from app.db import engine
from app.models import Dj, ScrapeStatus, Show, Station

logger = logging.getLogger("basealert.scraper")

HEADERS = {"User-Agent": "Mozilla/5.0 (compatible; BaseAlert/1.0; +https://github.com/)"}
DAYS_AHEAD = 6  # today + 6 following days (matches the site's own day-tab range)

# tb-group's own JSON showplan API - same data the stations' HTML pages
# render, but structured and without the DJ-id regex. Preferred source when
# a station has a known api_id; HTML scraping (below) is the fallback for
# when this API is unreachable or returns garbage.
API_BASE_URL = "https://api.tb-group.fm/v1/showplan"
BERLIN_TZ = ZoneInfo("Europe/Berlin")


def _parse_api_events(payload: list[dict]):
    """`s`/`e` are UTC epoch-ms; converted here to naive Europe/Berlin time
    to match the convention every other naive Show timestamp in this app
    uses (the container runs with TZ=Europe/Berlin, see docker-compose.yml)."""
    for item in payload:
        dj_name = item.get("m")
        if not dj_name:
            continue
        external_id = str(item["mi"]) if item.get("mi") is not None else None
        start_time = datetime.fromtimestamp(item["s"] / 1000, tz=BERLIN_TZ).replace(tzinfo=None)
        end_time = None
        if item.get("e") is not None:
            end_time = datetime.fromtimestamp(item["e"] / 1000, tz=BERLIN_TZ).replace(tzinfo=None)

        yield {
            "dj_name": dj_name,
            "external_id": external_id,
            "profile_path": f"/user/beschreibung?user={external_id}" if external_id else None,
            "show_name": item.get("n"),
            "genre": item.get("ss"),
            "start_time": start_time,
            "end_time": end_time,
            "image_url": None,
        }


def _parse_events(html: str):
    soup = BeautifulSoup(html, "html.parser")
    items = soup.select('div.item[itemtype="http://schema.org/BroadcastEvent"]')
    for item in items:
        start_span = item.select_one('span[itemprop="startDate"]')
        if not start_span or not start_span.get("content"):
            continue
        try:
            start_time = datetime.fromisoformat(start_span["content"])
        except ValueError:
            continue

        end_time = None
        title_h2 = item.select_one("h2.title")
        if title_h2:
            for span in title_h2.find_all("span"):
                if span is start_span:
                    continue
                if span.get("content"):
                    try:
                        end_time = datetime.fromisoformat(span["content"])
                    except ValueError:
                        pass
                    break

        dj_span = item.select_one('span[itemprop="dj"]')
        dj_name = dj_span.get_text(strip=True) if dj_span else None
        external_id = None
        profile_path = None
        if dj_span:
            link = dj_span.find("a")
            if link and link.get("href"):
                profile_path = link["href"]
                match = re.search(r"user=(\d+)", profile_path)
                if match:
                    external_id = match.group(1)

        show_name_span = item.select_one('span[itemprop="name"]')
        show_name = show_name_span.get_text(strip=True) if show_name_span else None

        genre_span = item.select_one('span[itemprop="genre"]')
        genre = genre_span.get_text(strip=True) if genre_span else None

        img = item.select_one("figure.image img")
        image_url = img["src"] if img and img.get("src") else None

        if not dj_name:
            continue

        yield {
            "dj_name": dj_name,
            "external_id": external_id,
            "profile_path": profile_path,
            "show_name": show_name,
            "genre": genre,
            "start_time": start_time,
            "end_time": end_time,
            "image_url": image_url,
        }


def _get_or_create_dj(session: Session, dj_cache: dict[str, Dj], event: dict) -> Dj:
    """DJs are tracked globally by name so the same person is recognized
    across all stations, not just the one they were first scraped from."""
    dj = dj_cache.get(event["dj_name"])
    if dj:
        if event["external_id"] and not dj.external_id:
            dj.external_id = event["external_id"]
            dj.profile_path = event["profile_path"]
            session.add(dj)
        return dj
    dj = Dj(
        name=event["dj_name"],
        external_id=event["external_id"],
        profile_path=event["profile_path"],
    )
    session.add(dj)
    session.flush()
    dj_cache[dj.name] = dj
    return dj


def _fetch_api_events(http: requests.Session, station: Station) -> list[dict]:
    """Fetches this station's schedule from the tb-group JSON API for today
    + DAYS_AHEAD days. Raises on the first failure so the caller falls back
    to HTML scraping instead of persisting a partial result (day N in the
    API is today + (N-1) days, confirmed against the stations' own sites)."""
    events = []
    for offset in range(DAYS_AHEAD + 1):
        resp = http.get(
            f"{API_BASE_URL}/{station.api_id}/{offset + 1}",
            headers=HEADERS,
            timeout=15,
        )
        resp.raise_for_status()
        events.extend(_parse_api_events(resp.json()))
    return events


def _fetch_html_events(http: requests.Session, station: Station) -> list[dict]:
    events = []
    today = datetime.now().date()
    for offset in range(DAYS_AHEAD + 1):
        day = today + timedelta(days=offset)
        day_param = day.strftime("%Y-%m-%d 00:00:00")
        try:
            resp = http.get(
                f"{station.base_url}/sendeplan",
                params={"station": station.key, "day": day_param},
                headers=HEADERS,
                timeout=15,
            )
            resp.raise_for_status()
        except requests.RequestException as exc:
            logger.warning("Failed to fetch %s day %s: %s", station.key, day_param, exc)
            continue
        events.extend(_parse_events(resp.text))
    return events


def scrape_station(
    session: Session,
    station: Station,
    http: requests.Session | None = None,
    dj_cache: dict[str, Dj] | None = None,
) -> int:
    """Fetch and persist the schedule for a single station.

    The tb-group JSON API is the primary source for stations with a known
    `api_id`; HTML scraping of the station's own /sendeplan page is the
    fallback, used when the API is unreachable/malformed or the station has
    no api_id at all.

    `http` and `dj_cache` can be shared across stations by the caller (see
    `scrape_all`) to reuse HTTP connections and avoid a DJ lookup query per
    event; if omitted, this creates and cleans up its own.
    """
    owns_http = http is None
    if http is None:
        http = requests.Session()
    if dj_cache is None:
        dj_cache = {dj.name: dj for dj in session.exec(select(Dj)).all()}

    count = 0
    existing_shows = {
        show.start_time: show
        for show in session.exec(select(Show).where(Show.station_id == station.id)).all()
    }

    try:
        events = None
        if station.api_id:
            try:
                events = _fetch_api_events(http, station)
            except (requests.RequestException, ValueError, KeyError) as exc:
                logger.warning(
                    "tb-group API failed for %s (%s), falling back to HTML scraping", station.key, exc
                )
        if events is None:
            events = _fetch_html_events(http, station)

        for event in events:
            dj = _get_or_create_dj(session, dj_cache, event)
            show = existing_shows.get(event["start_time"])
            if show:
                show.dj_id = dj.id
                show.show_name = event["show_name"]
                show.genre = event["genre"]
                show.end_time = event["end_time"]
                show.image_url = event["image_url"]
            else:
                show = Show(
                    station_id=station.id,
                    dj_id=dj.id,
                    show_name=event["show_name"],
                    genre=event["genre"],
                    start_time=event["start_time"],
                    end_time=event["end_time"],
                    image_url=event["image_url"],
                )
                existing_shows[event["start_time"]] = show
            session.add(show)
            count += 1
        session.commit()
    finally:
        if owns_http:
            http.close()
    return count


def _record_scrape_status(session: Session, station: Station, count: int | None, error: str | None) -> None:
    """Persists the outcome of a scrape attempt so the admin health page can
    show it, independent of whether this run's log lines are still around."""
    status = session.get(ScrapeStatus, station.id)
    if status is None:
        status = ScrapeStatus(station_id=station.id)
    status.last_attempt_at = datetime.now()
    if error is None:
        status.last_success_at = status.last_attempt_at
        status.last_error = None
        status.consecutive_errors = 0
        status.shows_scraped = count or 0
    else:
        status.last_error = error
        status.consecutive_errors += 1
    session.add(status)
    session.commit()


def scrape_all() -> dict:
    results = {}
    with Session(engine) as session:
        stations = session.exec(select(Station)).all()
        dj_cache = {dj.name: dj for dj in session.exec(select(Dj)).all()}
        with requests.Session() as http:
            for station in stations:
                try:
                    count = scrape_station(session, station, http=http, dj_cache=dj_cache)
                    results[station.key] = count
                    _record_scrape_status(session, station, count, error=None)
                except Exception as exc:
                    logger.exception("Error scraping %s", station.key)
                    results[station.key] = -1
                    _record_scrape_status(session, station, None, error=str(exc)[:500])
    return results
