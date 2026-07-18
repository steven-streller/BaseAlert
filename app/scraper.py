import logging
import re
from datetime import datetime, timedelta

import requests
from bs4 import BeautifulSoup
from sqlmodel import Session, select

from app.db import engine
from app.models import Dj, Show, Station

logger = logging.getLogger("basealert.scraper")

HEADERS = {"User-Agent": "Mozilla/5.0 (compatible; BaseAlert/1.0; +https://github.com/)"}
DAYS_AHEAD = 6  # today + 6 following days (matches the site's own day-tab range)


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


def _get_or_create_dj(session: Session, event: dict) -> Dj:
    """DJs are tracked globally by name so the same person is recognized
    across all stations, not just the one they were first scraped from."""
    dj = session.exec(select(Dj).where(Dj.name == event["dj_name"])).first()
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
    return dj


def scrape_station(session: Session, station: Station) -> int:
    count = 0
    today = datetime.now().date()
    for offset in range(DAYS_AHEAD + 1):
        day = today + timedelta(days=offset)
        day_param = day.strftime("%Y-%m-%d 00:00:00")
        try:
            resp = requests.get(
                f"{station.base_url}/sendeplan",
                params={"station": station.key, "day": day_param},
                headers=HEADERS,
                timeout=15,
            )
            resp.raise_for_status()
        except requests.RequestException as exc:
            logger.warning("Failed to fetch %s day %s: %s", station.key, day_param, exc)
            continue

        for event in _parse_events(resp.text):
            dj = _get_or_create_dj(session, event)
            show = session.exec(
                select(Show).where(
                    Show.station_id == station.id, Show.start_time == event["start_time"]
                )
            ).first()
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
            session.add(show)
            count += 1
    session.commit()
    return count


def scrape_all() -> dict:
    results = {}
    with Session(engine) as session:
        stations = session.exec(select(Station)).all()
        for station in stations:
            try:
                results[station.key] = scrape_station(session, station)
            except Exception:
                logger.exception("Error scraping %s", station.key)
                results[station.key] = -1
    return results
