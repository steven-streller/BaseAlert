import pytest
from fastapi.testclient import TestClient
from sqlmodel import Session, create_engine

from app.db import init_db


@pytest.fixture
def test_engine(tmp_path, monkeypatch):
    """A fresh, file-based SQLite engine per test, wired into every module
    that did `from app.db import engine` (a plain import binds the name at
    import time, so patching app.db.engine alone would not affect them)."""
    db_path = tmp_path / "test.db"
    engine = create_engine(f"sqlite:///{db_path}", connect_args={"check_same_thread": False})

    monkeypatch.setattr("app.db.DB_PATH", str(db_path))
    monkeypatch.setattr("app.db.engine", engine)
    monkeypatch.setattr("app.main.engine", engine)
    monkeypatch.setattr("app.scheduler.engine", engine)
    monkeypatch.setattr("app.auth.engine", engine)
    monkeypatch.setattr("app.scraper.engine", engine)

    init_db()
    return engine


@pytest.fixture
def client(test_engine, monkeypatch):
    """A TestClient over the real app, but WITHOUT running the startup event
    (that would start the scheduler - background threads + real network
    scraping - which tests neither need nor want). The DB is already
    initialized by the test_engine fixture above.

    reschedule_scrape_job talks to the live APScheduler job store, which only
    exists once start_scheduler() has run - stubbed out since that's a
    separate concern from what these route tests check.
    """
    from app.main import app

    monkeypatch.setattr("app.main.reschedule_scrape_job", lambda minutes: None)
    return TestClient(app)


def register(client: TestClient, email: str, password: str = "testpassword1"):
    return client.post(
        "/register",
        data={"email": email, "password": password, "password_confirm": password},
        follow_redirects=False,
    )


@pytest.fixture
def seed_station(test_engine):
    from app.models import Station

    station = Station(
        key="technobase.fm", name="TechnoBase.FM", base_url="https://www.technobase.fm", color="#ffc600"
    )
    with Session(test_engine) as session:
        session.add(station)
        session.commit()
        session.refresh(station)
    return station


@pytest.fixture
def seed_dj(test_engine):
    from app.models import Dj

    dj = Dj(name="DJ Testo")
    with Session(test_engine) as session:
        session.add(dj)
        session.commit()
        session.refresh(dj)
    return dj
