import logging
import os

from sqlmodel import Session, SQLModel, create_engine, select, text

from app.models import Setting, Station, User, UserSetting
from app.security import hash_password

logger = logging.getLogger("basealert.db")

DB_PATH = os.environ.get("BASEALERT_DB_PATH", "/app/data/basealert.db")
engine = create_engine(f"sqlite:///{DB_PATH}", connect_args={"check_same_thread": False})

STATIONS = [
    {"key": "technobase.fm", "name": "TechnoBase.FM", "base_url": "https://www.technobase.fm", "color": "#ffc600"},
    {"key": "housetime.fm", "name": "HouseTime.FM", "base_url": "https://www.housetime.fm", "color": "#00aeef"},
    {"key": "hardbase.fm", "name": "HardBase.FM", "base_url": "https://www.hardbase.fm", "color": "#e2001a"},
    {"key": "trancebase.fm", "name": "TranceBase.FM", "base_url": "https://www.trancebase.fm", "color": "#8dc63f"},
]

# Shared across every user - scraping the schedules isn't a per-user concern.
GLOBAL_DEFAULT_SETTINGS = {
    "scrape_interval_minutes": "60",
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


def _table_columns(table: str) -> set[str]:
    with engine.connect() as conn:
        columns = conn.execute(text(f"PRAGMA table_info({table})")).fetchall()
    return {col[1] for col in columns}


def _dj_table_has_station_id() -> bool:
    return "station_id" in _table_columns("dj")


def _migrate_dj_to_global() -> None:
    """Older versions kept one Dj row per station. Merge those into one global
    Dj per name (by name, dropping station_id) so favorites apply across all
    stations, while keeping the shows/favorites pointed at the surviving row."""
    if not _dj_table_has_station_id():
        return
    logger.info("Migrating dj table to global (per-name) identity")
    with engine.connect() as conn:
        conn.execute(text("ALTER TABLE dj RENAME TO dj_migrating_old"))
        conn.commit()

    SQLModel.metadata.create_all(engine)

    with engine.connect() as conn:
        conn.execute(
            text(
                "INSERT INTO dj (id, name, external_id, profile_path) "
                "SELECT MIN(id), name, MAX(external_id), MAX(profile_path) "
                "FROM dj_migrating_old GROUP BY name"
            )
        )
        conn.execute(
            text(
                "UPDATE show SET dj_id = ("
                "  SELECT d.id FROM dj d JOIN dj_migrating_old o ON o.name = d.name WHERE o.id = show.dj_id"
                ") WHERE dj_id IS NOT NULL"
            )
        )
        conn.execute(
            text(
                "UPDATE OR IGNORE favorite SET dj_id = ("
                "  SELECT d.id FROM dj d JOIN dj_migrating_old o ON o.name = d.name WHERE o.id = favorite.dj_id"
                ")"
            )
        )
        conn.execute(text("DELETE FROM favorite WHERE dj_id NOT IN (SELECT id FROM dj)"))
        conn.execute(text("DROP TABLE dj_migrating_old"))
        conn.commit()


def _migrate_to_multi_user() -> None:
    """Older versions had no accounts: one global set of favorites and one
    global set of notification settings. Move those onto a real User so
    logins work going forward.

    If BASEALERT_INITIAL_USER_EMAIL/_PASSWORD are set, that data is migrated
    onto a freshly created account. Otherwise it's dropped - there is no
    account to attach it to, and the old rows can't be logged into anyway.
    """
    favorite_columns = _table_columns("favorite")
    if not favorite_columns or "user_id" in favorite_columns:
        return  # fresh install, or already migrated

    logger.info("Migrating favorites/settings to the multi-user schema")
    notificationlog_had_user_id = "user_id" in _table_columns("notificationlog")

    with engine.connect() as conn:
        conn.execute(text("ALTER TABLE favorite RENAME TO favorite_migrating_old"))
        if not notificationlog_had_user_id:
            conn.execute(text("ALTER TABLE notificationlog RENAME TO notificationlog_migrating_old"))
        conn.commit()

    SQLModel.metadata.create_all(engine)

    email = os.environ.get("BASEALERT_INITIAL_USER_EMAIL")
    password = os.environ.get("BASEALERT_INITIAL_USER_PASSWORD")

    with Session(engine) as session:
        user = None
        if email and password:
            user = session.exec(select(User).where(User.email == email)).first()
            if not user:
                user = User(email=email, password_hash=hash_password(password))
                session.add(user)
                session.commit()
                session.refresh(user)
                logger.info("Created initial user %s for migrated data", email)
        else:
            logger.warning(
                "No BASEALERT_INITIAL_USER_EMAIL/_PASSWORD set - "
                "existing favorites and notification settings will not be migrated"
            )

    with engine.connect() as conn:
        if user:
            conn.execute(
                text("INSERT INTO favorite (user_id, dj_id) SELECT :user_id, dj_id FROM favorite_migrating_old"),
                {"user_id": user.id},
            )
            if not notificationlog_had_user_id:
                conn.execute(
                    text(
                        "INSERT INTO notificationlog (user_id, show_id, notified_at) "
                        "SELECT :user_id, show_id, notified_at FROM notificationlog_migrating_old"
                    ),
                    {"user_id": user.id},
                )
            for key in USER_DEFAULT_SETTINGS:
                existing = conn.execute(
                    text("SELECT value FROM setting WHERE key = :key"), {"key": key}
                ).first()
                if existing is not None:
                    conn.execute(
                        text(
                            "INSERT INTO usersetting (user_id, key, value) VALUES (:user_id, :key, :value)"
                        ),
                        {"user_id": user.id, "key": key, "value": existing[0]},
                    )
                    conn.execute(text("DELETE FROM setting WHERE key = :key"), {"key": key})
        conn.execute(text("DROP TABLE favorite_migrating_old"))
        if not notificationlog_had_user_id:
            conn.execute(text("DROP TABLE notificationlog_migrating_old"))
        conn.commit()


def init_db() -> None:
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
    _migrate_dj_to_global()
    _migrate_to_multi_user()
    SQLModel.metadata.create_all(engine)
    with Session(engine) as session:
        for station in STATIONS:
            existing = session.exec(select(Station).where(Station.key == station["key"])).first()
            if not existing:
                session.add(Station(**station))
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


def get_user_setting(session: Session, user_id: int, key: str) -> str:
    setting = session.exec(
        select(UserSetting).where(UserSetting.user_id == user_id, UserSetting.key == key)
    ).first()
    return setting.value if setting else USER_DEFAULT_SETTINGS.get(key, "")


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
