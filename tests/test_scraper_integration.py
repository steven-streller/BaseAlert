from pathlib import Path

from sqlmodel import Session, select

from app.models import Dj, Show, Station
from app.scraper import scrape_station

FIXTURE = (Path(__file__).parent / "fixtures" / "sendeplan_sample.html").read_text()


class _FakeResponse:
    def __init__(self, text: str):
        self.text = text

    def raise_for_status(self) -> None:
        pass


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
