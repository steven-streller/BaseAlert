from sqlmodel import Session, SQLModel, create_engine

from app.db import set_user_setting
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
    assert enabled_channels(session, user_id=1) == []


def test_enabled_channels_respects_setting():
    session = make_session()
    set_user_setting(session, 1, "ntfy_enabled", "true")
    set_user_setting(session, 1, "ntfy_server_url", "https://ntfy.sh")
    set_user_setting(session, 1, "ntfy_topic", "test-topic")
    assert enabled_channels(session, user_id=1) == ["ntfy"]


def test_notify_all_reports_false_for_unconfigured_enabled_channel():
    session = make_session()
    set_user_setting(session, 1, "discord_enabled", "true")
    # discord_webhook_url intentionally left blank -> must fail without a network call
    results = notify_all(session, 1, "Titel", "Nachricht")
    assert results == {"discord": False}


def test_user_settings_are_isolated_between_users():
    session = make_session()
    set_user_setting(session, 1, "ntfy_enabled", "true")
    set_user_setting(session, 1, "ntfy_server_url", "https://ntfy.sh")
    set_user_setting(session, 1, "ntfy_topic", "user-1-topic")

    # user 2 never configured anything - must not see user 1's channel or settings
    assert enabled_channels(session, user_id=2) == []
    assert enabled_channels(session, user_id=1) == ["ntfy"]
