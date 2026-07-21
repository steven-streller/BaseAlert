import os

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
    },
    {
        "key": "housetime.fm",
        "name": "HouseTime.FM",
        "base_url": "https://www.housetime.fm",
        "color": "#00aeef",
    },
    {"key": "hardbase.fm", "name": "HardBase.FM", "base_url": "https://www.hardbase.fm", "color": "#e2001a"},
    {
        "key": "trancebase.fm",
        "name": "TranceBase.FM",
        "base_url": "https://www.trancebase.fm",
        "color": "#8dc63f",
    },
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


def init_db() -> None:
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
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
