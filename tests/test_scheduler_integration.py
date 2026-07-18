from datetime import datetime, timedelta

from sqlmodel import Session, select

from app.models import Dj, Favorite, ListeningWindow, NotificationLog, Show, Station, User
from app.scheduler import _notify_user


def _make_user_station_dj_show(session, *, start_offset_minutes=5, dj_name="DJ Testo"):
    # init_db() already seeds the 4 real stations - reuse one instead of
    # inserting a duplicate "technobase.fm" key.
    station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
    user = User(email="user@example.com", password_hash="hash")
    dj = Dj(name=dj_name)
    session.add_all([user, dj])
    session.commit()
    session.refresh(user)
    session.refresh(dj)

    show = Show(
        station_id=station.id,
        dj_id=dj.id,
        show_name="Test Show",
        start_time=datetime.now() + timedelta(minutes=start_offset_minutes),
    )
    session.add(show)
    session.commit()
    session.refresh(show)
    return user, station, dj, show


def test_notify_user_sends_for_favorite_and_dedups(test_engine, monkeypatch):
    monkeypatch.setattr("app.scheduler.enabled_channels", lambda session, user_id: ["fake"])
    calls = []
    monkeypatch.setattr(
        "app.scheduler.notify_all",
        lambda session, user_id, title, message, url=None: (calls.append(title) or {"fake": True}),
    )

    with Session(test_engine) as session:
        user, station, dj, show = _make_user_station_dj_show(session)
        session.add(Favorite(user_id=user.id, dj_id=dj.id))
        session.commit()

        now = datetime.now()
        _notify_user(session, user, now)

        logs = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert len(logs) == 1
        assert logs[0].show_id == show.id
        assert "legt gleich auf" in calls[0]

        # second run must not notify again for the same show
        _notify_user(session, user, now)
        logs_after = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert len(logs_after) == 1
        assert len(calls) == 1


def test_notify_user_sends_for_listening_window_when_not_favorite(test_engine, monkeypatch):
    monkeypatch.setattr("app.scheduler.enabled_channels", lambda session, user_id: ["fake"])
    calls = []
    monkeypatch.setattr(
        "app.scheduler.notify_all",
        lambda session, user_id, title, message, url=None: (calls.append(title) or {"fake": True}),
    )

    with Session(test_engine) as session:
        user, station, dj, show = _make_user_station_dj_show(session)
        now = datetime.now()
        window = ListeningWindow(
            user_id=user.id,
            weekdays=str(now.weekday()),
            start_time=(now - timedelta(minutes=1)).time(),
            end_time=(now + timedelta(hours=2)).time(),
        )
        session.add(window)
        session.commit()

        _notify_user(session, user, now)

        logs = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert len(logs) == 1
        assert "Live gleich" in calls[0]


def test_notify_user_does_not_send_without_favorite_or_window(test_engine, monkeypatch):
    monkeypatch.setattr("app.scheduler.enabled_channels", lambda session, user_id: ["fake"])
    monkeypatch.setattr("app.scheduler.notify_all", lambda *a, **k: {"fake": True})

    with Session(test_engine) as session:
        user, _station, _dj, _show = _make_user_station_dj_show(session)
        _notify_user(session, user, datetime.now())
        logs = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert logs == []


def test_notify_user_skips_when_no_channel_enabled(test_engine, monkeypatch):
    monkeypatch.setattr("app.scheduler.enabled_channels", lambda session, user_id: [])
    called = []
    monkeypatch.setattr("app.scheduler.notify_all", lambda *a, **k: called.append(1) or {})

    with Session(test_engine) as session:
        user, station, dj, show = _make_user_station_dj_show(session)
        session.add(Favorite(user_id=user.id, dj_id=dj.id))
        session.commit()

        _notify_user(session, user, datetime.now())
        assert called == []
        logs = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert logs == []


def test_notify_user_ignores_show_outside_lead_window(test_engine, monkeypatch):
    monkeypatch.setattr("app.scheduler.enabled_channels", lambda session, user_id: ["fake"])
    monkeypatch.setattr("app.scheduler.notify_all", lambda *a, **k: {"fake": True})

    with Session(test_engine) as session:
        # default notify_lead_minutes is 15; a show 2h out must not trigger yet
        user, station, dj, show = _make_user_station_dj_show(session, start_offset_minutes=120)
        session.add(Favorite(user_id=user.id, dj_id=dj.id))
        session.commit()

        _notify_user(session, user, datetime.now())
        logs = session.exec(select(NotificationLog).where(NotificationLog.user_id == user.id)).all()
        assert logs == []
