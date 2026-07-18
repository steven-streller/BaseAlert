from datetime import datetime
from typing import Optional

from sqlmodel import Field, SQLModel, UniqueConstraint


class Station(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    key: str = Field(unique=True, index=True)  # e.g. "technobase.fm"
    name: str  # e.g. "TechnoBase.FM"
    base_url: str  # e.g. "https://www.technobase.fm"
    color: str = "#ffc600"


class Dj(SQLModel, table=True):
    """A DJ is tracked globally by name, independent of which station(s) play them."""

    id: Optional[int] = Field(default=None, primary_key=True)
    name: str = Field(index=True, unique=True)
    external_id: Optional[str] = None
    profile_path: Optional[str] = None


class Show(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    station_id: int = Field(foreign_key="station.id", index=True)
    dj_id: Optional[int] = Field(default=None, foreign_key="dj.id", index=True)
    show_name: Optional[str] = None
    genre: Optional[str] = None
    start_time: datetime = Field(index=True)
    end_time: Optional[datetime] = None
    image_url: Optional[str] = None


class User(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    email: str = Field(index=True, unique=True)
    password_hash: str
    created_at: datetime = Field(default_factory=datetime.utcnow)


class Favorite(SQLModel, table=True):
    __table_args__ = (UniqueConstraint("user_id", "dj_id"),)

    id: Optional[int] = Field(default=None, primary_key=True)
    user_id: int = Field(foreign_key="user.id", index=True)
    dj_id: int = Field(foreign_key="dj.id", index=True)


class NotificationLog(SQLModel, table=True):
    __table_args__ = (UniqueConstraint("user_id", "show_id"),)

    id: Optional[int] = Field(default=None, primary_key=True)
    user_id: int = Field(foreign_key="user.id", index=True)
    show_id: int = Field(foreign_key="show.id", index=True)
    notified_at: datetime = Field(default_factory=datetime.utcnow)


class Setting(SQLModel, table=True):
    """Global settings shared by all users (currently just the scrape interval)."""

    key: str = Field(primary_key=True)
    value: str


class UserSetting(SQLModel, table=True):
    """Per-user settings: notification channel config + notify lead time."""

    __table_args__ = (UniqueConstraint("user_id", "key"),)

    id: Optional[int] = Field(default=None, primary_key=True)
    user_id: int = Field(foreign_key="user.id", index=True)
    key: str = Field(index=True)
    value: str
