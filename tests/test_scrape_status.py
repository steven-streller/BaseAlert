import json
from pathlib import Path

from sqlmodel import Session, select

from app.models import ScrapeStatus
from app.scraper import scrape_all

FIXTURE = (Path(__file__).parent / "fixtures" / "sendeplan_sample.html").read_text()


class _FakeResponse:
    def __init__(self, text: str):
        self.text = text

    def raise_for_status(self) -> None:
        pass

    def json(self):
        # all seeded stations have an api_id, so the API is tried first; this
        # HTML fixture isn't valid JSON, which makes scrape_station fall back
        # to HTML scraping - same as the tb-group API being unreachable.
        return json.loads(self.text)


def test_scrape_all_records_success_status(test_engine, monkeypatch):
    monkeypatch.setattr("app.scraper.requests.Session.get", lambda self, *a, **k: _FakeResponse(FIXTURE))

    scrape_all()

    with Session(test_engine) as session:
        statuses = session.exec(select(ScrapeStatus)).all()
        assert len(statuses) == 4  # one per seeded station
        for status in statuses:
            assert status.last_error is None
            assert status.last_success_at is not None
            # scrape_station's count is per (day x event) fetch, not deduped shows
            assert status.shows_scraped == 14
            assert status.consecutive_errors == 0


def test_scrape_all_records_error_status_and_counts_consecutive_failures(test_engine, monkeypatch):
    def _boom(*args, **kwargs):
        raise RuntimeError("station is down")

    monkeypatch.setattr("app.scraper.scrape_station", _boom)

    scrape_all()
    scrape_all()

    with Session(test_engine) as session:
        statuses = session.exec(select(ScrapeStatus)).all()
        assert len(statuses) == 4
        for status in statuses:
            assert status.last_error == "station is down"
            assert status.consecutive_errors == 2
            assert status.last_success_at is None


def test_scrape_all_resets_error_streak_after_recovery(test_engine, monkeypatch):
    def _boom(*args, **kwargs):
        raise RuntimeError("station is down")

    def _ok(session, station, **kwargs):
        return 14

    monkeypatch.setattr("app.scraper.scrape_station", _boom)
    scrape_all()

    monkeypatch.setattr("app.scraper.scrape_station", _ok)
    scrape_all()

    with Session(test_engine) as session:
        statuses = session.exec(select(ScrapeStatus)).all()
        for status in statuses:
            assert status.last_error is None
            assert status.consecutive_errors == 0
            assert status.last_success_at is not None
            assert status.shows_scraped == 14
