from datetime import datetime
from pathlib import Path

from app.scraper import _parse_events

FIXTURE = (Path(__file__).parent / "fixtures" / "sendeplan_sample.html").read_text()


def test_parse_events_extracts_all_fields():
    events = list(_parse_events(FIXTURE))
    assert len(events) == 2

    first = events[0]
    assert first["dj_name"] == "DJ Salvatore"
    assert first["show_name"] == "Blast From The Past"
    assert first["genre"] == "Hands Up / Dance"
    assert first["external_id"] == "506199"
    assert first["profile_path"] == "/user/beschreibung?user=506199"
    assert first["start_time"] == datetime(2026, 7, 18, 18, 0)
    assert first["end_time"] == datetime(2026, 7, 18, 21, 0)


def test_parse_events_handles_dj_without_profile_link_and_missing_genre():
    events = list(_parse_events(FIXTURE))
    second = events[1]
    assert second["dj_name"] == "Guest Mix"
    assert second["external_id"] is None
    assert second["profile_path"] is None
    assert second["genre"] is None
    assert second["start_time"] == datetime(2026, 7, 18, 22, 0)
    assert second["end_time"] == datetime(2026, 7, 19, 0, 0)


def test_parse_events_returns_empty_for_page_without_schedule():
    assert list(_parse_events("<html><body>Keine Sendungen heute.</body></html>")) == []
