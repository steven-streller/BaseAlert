import os
import secrets

from sqlmodel import Session, SQLModel, create_engine, select

from app.models import Setting, Station, UserSetting

DB_PATH = os.environ.get("BASEALERT_DB_PATH", "/app/data/basealert.db")
engine = create_engine(f"sqlite:///{DB_PATH}", connect_args={"check_same_thread": False})

STATIONS = [
    {
        "key": "technobase.fm",
        "name": "TechnoBase.FM",
        "base_url": "https://www.technobase.fm",
        "color": "#ffc600",
        "api_id": 5,
    },
    {
        "key": "housetime.fm",
        "name": "HouseTime.FM",
        "base_url": "https://www.housetime.fm",
        "color": "#00aeef",
        "api_id": 6,
    },
    {
        "key": "hardbase.fm",
        "name": "HardBase.FM",
        "base_url": "https://www.hardbase.fm",
        "color": "#e2001a",
        "api_id": 7,
    },
    {
        "key": "trancebase.fm",
        "name": "TranceBase.FM",
        "base_url": "https://www.trancebase.fm",
        "color": "#8dc63f",
        "api_id": 8,
    },
]

# Shared across every user - scraping the schedules isn't a per-user concern.
GLOBAL_DEFAULT_SETTINGS = {
    "scrape_interval_minutes": "60",
    # Read once as the initial value on first startup (same idea as
    # BASEALERT_DB_PATH above) - after that first seed this Setting row is
    # the source of truth and is toggled live from the admin settings page,
    # so the env var is no longer consulted.
    "registration_enabled": "true"
    if os.environ.get("REGISTRATION_ENABLED", "true").lower() != "false"
    else "false",
}

# Each user gets their own copy of these: notification channels + lead time.
USER_DEFAULT_SETTINGS = {
    "notify_lead_minutes": "15",
    # Pushover
    "pushover_enabled": "false",
    "pushover_user_key": "",
    "pushover_api_token": "",
    # ntfy
    "ntfy_enabled": "false",
    "ntfy_server_url": "https://ntfy.sh",
    "ntfy_topic": "",
    "ntfy_token": "",
    # Telegram
    "telegram_enabled": "false",
    "telegram_bot_token": "",
    "telegram_chat_id": "",
    # Discord
    "discord_enabled": "false",
    "discord_webhook_url": "",
    # Generic webhook
    "webhook_enabled": "false",
    "webhook_url": "",
    # Email
    "email_enabled": "false",
    "email_smtp_host": "",
    "email_smtp_port": "587",
    "email_smtp_user": "",
    "email_smtp_password": "",
    "email_from": "",
    "email_to": "",
    "email_use_tls": "true",
}


def _ensure_user_admin_column() -> None:
    """Add `is_admin` to pre-existing `user` tables. `create_all` only creates
    missing tables, not missing columns on tables that already exist, so
    installs that predate the admin flag need this one-off ALTER TABLE."""
    with engine.connect() as conn:
        columns = {row[1] for row in conn.exec_driver_sql("PRAGMA table_info(user)").fetchall()}
        if "is_admin" not in columns:
            conn.exec_driver_sql("ALTER TABLE user ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT 0")
            conn.commit()


def _ensure_station_api_id_column() -> None:
    """Add `api_id` to pre-existing `station` tables and backfill it from
    STATIONS. `create_all` only creates missing tables/columns for brand-new
    installs - existing rows need this column added and populated so the
    scraper can start using the tb-group JSON API without a fresh DB."""
    with engine.connect() as conn:
        columns = {row[1] for row in conn.exec_driver_sql("PRAGMA table_info(station)").fetchall()}
        if "api_id" not in columns:
            conn.exec_driver_sql("ALTER TABLE station ADD COLUMN api_id INTEGER")
            for station in STATIONS:
                conn.exec_driver_sql(
                    "UPDATE station SET api_id = ? WHERE key = ?",
                    (station["api_id"], station["key"]),
                )
            conn.commit()


def init_db() -> None:
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
    SQLModel.metadata.create_all(engine)
    _ensure_user_admin_column()
    _ensure_station_api_id_column()
    with Session(engine) as session:
        for station in STATIONS:
            existing = session.exec(select(Station).where(Station.key == station["key"])).first()
            if not existing:
                session.add(Station(**station))
            elif existing.api_id != station["api_id"]:
                existing.api_id = station["api_id"]
                session.add(existing)
        for key, value in GLOBAL_DEFAULT_SETTINGS.items():
            existing = session.exec(select(Setting).where(Setting.key == key)).first()
            if not existing:
                session.add(Setting(key=key, value=value))
        session.commit()


def get_setting(session: Session, key: str) -> str:
    setting = session.exec(select(Setting).where(Setting.key == key)).first()
    return setting.value if setting else GLOBAL_DEFAULT_SETTINGS.get(key, "")


def set_setting(session: Session, key: str, value: str) -> None:
    setting = session.exec(select(Setting).where(Setting.key == key)).first()
    if setting:
        setting.value = value
        session.add(setting)
    else:
        session.add(Setting(key=key, value=value))
    session.commit()


def get_or_create_session_secret(session: Session) -> str:
    """Persists a random session-signing secret in Settings on first run, so
    session cookies survive restarts even without SESSION_SECRET_KEY set.
    An explicit SESSION_SECRET_KEY env var still takes precedence - callers
    check that first and only fall back to this (e.g. so several Kubernetes
    replicas can share one signing key via a shared env var).
    """
    existing = get_setting(session, "session_secret_key")
    if existing:
        return existing
    value = secrets.token_hex(32)
    set_setting(session, "session_secret_key", value)
    return value


def regenerate_session_secret(session: Session) -> str:
    """Rotates the DB-stored session secret, invalidating every existing
    session cookie. Only takes effect after the app restarts - the signing
    key used by the running process is fixed at startup."""
    value = secrets.token_hex(32)
    set_setting(session, "session_secret_key", value)
    return value


def get_user_setting(session: Session, user_id: int, key: str) -> str:
    setting = session.exec(
        select(UserSetting).where(UserSetting.user_id == user_id, UserSetting.key == key)
    ).first()
    return setting.value if setting else USER_DEFAULT_SETTINGS.get(key, "")


def get_user_settings(session: Session, user_id: int) -> dict[str, str]:
    """Loads all of a user's settings in a single query, merged over the defaults.

    Use this instead of calling `get_user_setting` per key when several keys
    are needed at once (e.g. checking all notification channels) - avoids one
    query per key.
    """
    rows = session.exec(select(UserSetting).where(UserSetting.user_id == user_id)).all()
    return {**USER_DEFAULT_SETTINGS, **{row.key: row.value for row in rows}}


def set_user_setting(session: Session, user_id: int, key: str, value: str) -> None:
    setting = session.exec(
        select(UserSetting).where(UserSetting.user_id == user_id, UserSetting.key == key)
    ).first()
    if setting:
        setting.value = value
        session.add(setting)
    else:
        session.add(UserSetting(user_id=user_id, key=key, value=value))
    session.commit()
