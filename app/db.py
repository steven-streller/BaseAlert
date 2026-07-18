import logging
import os

from sqlmodel import Session, SQLModel, create_engine, select, text

from app.models import Setting, Station

logger = logging.getLogger("basealert.db")

DB_PATH = os.environ.get("BASEALERT_DB_PATH", "/app/data/basealert.db")
engine = create_engine(f"sqlite:///{DB_PATH}", connect_args={"check_same_thread": False})

STATIONS = [
    {"key": "technobase.fm", "name": "TechnoBase.FM", "base_url": "https://www.technobase.fm", "color": "#ffc600"},
    {"key": "housetime.fm", "name": "HouseTime.FM", "base_url": "https://www.housetime.fm", "color": "#00aeef"},
    {"key": "hardbase.fm", "name": "HardBase.FM", "base_url": "https://www.hardbase.fm", "color": "#e2001a"},
    {"key": "trancebase.fm", "name": "TranceBase.FM", "base_url": "https://www.trancebase.fm", "color": "#8dc63f"},
]

DEFAULT_SETTINGS = {
    "scrape_interval_minutes": "60",
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


def _dj_table_has_station_id() -> bool:
    with engine.connect() as conn:
        columns = conn.execute(text("PRAGMA table_info(dj)")).fetchall()
    return any(col[1] == "station_id" for col in columns)


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


def init_db() -> None:
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
    _migrate_dj_to_global()
    SQLModel.metadata.create_all(engine)
    with Session(engine) as session:
        for station in STATIONS:
            existing = session.exec(select(Station).where(Station.key == station["key"])).first()
            if not existing:
                session.add(Station(**station))
        for key, value in DEFAULT_SETTINGS.items():
            existing = session.exec(select(Setting).where(Setting.key == key)).first()
            if not existing:
                session.add(Setting(key=key, value=value))
        session.commit()


def get_setting(session: Session, key: str) -> str:
    setting = session.exec(select(Setting).where(Setting.key == key)).first()
    return setting.value if setting else DEFAULT_SETTINGS.get(key, "")


def set_setting(session: Session, key: str, value: str) -> None:
    setting = session.exec(select(Setting).where(Setting.key == key)).first()
    if setting:
        setting.value = value
        session.add(setting)
    else:
        session.add(Setting(key=key, value=value))
    session.commit()
