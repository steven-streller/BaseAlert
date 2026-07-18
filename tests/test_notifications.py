from sqlmodel import Session, SQLModel, create_engine

from app.db import set_setting
from app.notifications import CHANNELS, enabled_channels, notify_all


def make_session() -> Session:
    engine = create_engine("sqlite://")
    SQLModel.metadata.create_all(engine)
    return Session(engine)


def test_channel_registry_keys_match_declared_fields():
    for channel in CHANNELS.values():
        assert channel["keys"] == [field[0] for field in channel["fields"]]
        assert callable(channel["send"])


def test_enabled_channels_empty_by_default():
    session = make_session()
    assert enabled_channels(session) == []


def test_enabled_channels_respects_setting():
    session = make_session()
    set_setting(session, "ntfy_enabled", "true")
    set_setting(session, "ntfy_server_url", "https://ntfy.sh")
    set_setting(session, "ntfy_topic", "test-topic")
    assert enabled_channels(session) == ["ntfy"]


def test_notify_all_reports_false_for_unconfigured_enabled_channel():
    session = make_session()
    set_setting(session, "discord_enabled", "true")
    # discord_webhook_url intentionally left blank -> must fail without a network call
    results = notify_all(session, "Titel", "Nachricht")
    assert results == {"discord": False}
