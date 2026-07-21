import json
from pathlib import Path
from urllib.parse import urlparse

import requests
from sqlmodel import Session, select

from app.models import Dj, Show, Station
from app.scraper import scrape_station

FIXTURE = (Path(__file__).parent / "fixtures" / "sendeplan_sample.html").read_text()


class _FakeResponse:
    def __init__(self, text: str):
        self.text = text

    def raise_for_status(self) -> None:
        pass

    def json(self):
        # station fixtures here are HTML, not JSON - mirrors how the real
        # tb-group API failing/returning garbage makes scrape_station fall
        # back to HTML scraping (all seeded stations have an api_id, so the
        # API is always tried first).
        return json.loads(self.text)


def _stub_requests_get(monkeypatch, text: str = FIXTURE):
    monkeypatch.setattr("app.scraper.requests.Session.get", lambda self, *a, **k: _FakeResponse(text))


def test_scrape_station_creates_djs_and_shows(test_engine, monkeypatch):
    _stub_requests_get(monkeypatch)

    with Session(test_engine) as session:
        station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        scrape_station(session, station)

        djs = session.exec(select(Dj)).all()
        shows = session.exec(select(Show)).all()

        # the fixture has 2 events; DAYS_AHEAD+1 identical fake responses must
        # not create duplicates since every "day" resolves to the same
        # (station_id, start_time) pairs
        assert {d.name for d in djs} == {"DJ Salvatore", "Guest Mix"}
        assert len(shows) == 2

        salvatore = next(d for d in djs if d.name == "DJ Salvatore")
        assert salvatore.external_id == "506199"


def test_scrape_station_rerun_updates_instead_of_duplicating(test_engine, monkeypatch):
    _stub_requests_get(monkeypatch)

    with Session(test_engine) as session:
        station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        scrape_station(session, station)
        scrape_station(session, station)

        shows = session.exec(select(Show)).all()
        assert len(shows) == 2


def test_scrape_station_dedups_dj_by_name_across_stations(test_engine, monkeypatch):
    _stub_requests_get(monkeypatch)

    with Session(test_engine) as session:
        technobase = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        housetime = session.exec(select(Station).where(Station.key == "housetime.fm")).first()

        scrape_station(session, technobase)
        scrape_station(session, housetime)

        djs = session.exec(select(Dj).where(Dj.name == "DJ Salvatore")).all()
        assert len(djs) == 1

        shows = session.exec(select(Show)).all()
        # same DJ, same start times, but two different stations -> 2+2 shows
        assert len(shows) == 4


class _ApiResponse:
    def __init__(self, payload: list[dict]):
        self._payload = payload

    def raise_for_status(self) -> None:
        pass

    def json(self):
        return self._payload


def test_scrape_station_prefers_api_over_html(test_engine, monkeypatch):
    api_payload = [
        {
            "n": "HHC CULTURE",
            "m": "RØMAN_G",
            "mi": 553347,
            "ss": "Happy Hardcore",
            "s": 1768471200000,
            "e": 1768478400000,
        }
    ]

    def _get(self, url, *args, **kwargs):
        assert urlparse(url).hostname == "api.tb-group.fm"
        return _ApiResponse(api_payload)

    monkeypatch.setattr("app.scraper.requests.Session.get", _get)

    with Session(test_engine) as session:
        station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        assert station.api_id == 5  # seeded by STATIONS in app/db.py
        scrape_station(session, station)

        djs = session.exec(select(Dj)).all()
        shows = session.exec(select(Show)).all()

        # 7 identical daily API responses for the same show -> deduped to 1 row
        assert len(shows) == 1
        assert djs[0].name == "RØMAN_G"
        assert djs[0].external_id == "553347"


def test_scrape_station_falls_back_to_html_when_api_unreachable(test_engine, monkeypatch):
    def _get(self, url, *args, **kwargs):
        if urlparse(url).hostname == "api.tb-group.fm":
            raise requests.exceptions.ConnectionError("tb-group API is down")
        return _FakeResponse(FIXTURE)

    monkeypatch.setattr("app.scraper.requests.Session.get", _get)

    with Session(test_engine) as session:
        station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        scrape_station(session, station)

        djs = session.exec(select(Dj)).all()
        shows = session.exec(select(Show)).all()
        assert {d.name for d in djs} == {"DJ Salvatore", "Guest Mix"}
        assert len(shows) == 2


def test_scrape_station_uses_html_when_station_has_no_api_id(test_engine, monkeypatch):
    _stub_requests_get(monkeypatch)

    with Session(test_engine) as session:
        station = Station(key="custom.fm", name="Custom", base_url="https://custom.fm", api_id=None)
        session.add(station)
        session.commit()
        session.refresh(station)

        scrape_station(session, station)

        shows = session.exec(select(Show).where(Show.station_id == station.id)).all()
        assert len(shows) == 2
