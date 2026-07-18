import logging
from datetime import datetime, timedelta

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.interval import IntervalTrigger
from sqlmodel import Session, select

from app.db import engine, get_setting, get_user_setting
from app.models import Dj, Favorite, NotificationLog, Show, Station, User
from app.notifications import enabled_channels, notify_all
from app.scraper import scrape_all

logger = logging.getLogger("basealert.scheduler")

scheduler = BackgroundScheduler()


def scrape_job() -> None:
    logger.info("Running scheduled scrape")
    results = scrape_all()
    logger.info("Scrape results: %s", results)


def _notify_user(session: Session, user: User, now: datetime) -> None:
    if not enabled_channels(session, user.id):
        return
    lead_minutes = int(get_user_setting(session, user.id, "notify_lead_minutes") or 15)
    window_end = now + timedelta(minutes=lead_minutes)

    favorite_dj_ids = set(session.exec(select(Favorite.dj_id).where(Favorite.user_id == user.id)).all())
    if not favorite_dj_ids:
        return

    already_notified = set(
        session.exec(select(NotificationLog.show_id).where(NotificationLog.user_id == user.id)).all()
    )

    upcoming_shows = session.exec(
        select(Show).where(Show.start_time >= now, Show.start_time <= window_end)
    ).all()

    for show in upcoming_shows:
        if show.id in already_notified or show.dj_id not in favorite_dj_ids:
            continue
        dj = session.get(Dj, show.dj_id)
        station = session.get(Station, show.station_id)
        title = f"{dj.name} legt gleich auf!"
        message = (
            f"{show.show_name or 'Show'} auf {station.name} "
            f"um {show.start_time.strftime('%H:%M')} Uhr"
            + (f" ({show.genre})" if show.genre else "")
        )
        results = notify_all(session, user.id, title, message, url=station.base_url)
        if any(results.values()):
            session.add(NotificationLog(user_id=user.id, show_id=show.id))
            session.commit()
            logger.info(
                "Notified %s for %s on %s at %s via %s",
                user.email,
                dj.name,
                station.key,
                show.start_time,
                [c for c, ok in results.items() if ok],
            )


def notify_check_job() -> None:
    now = datetime.now()
    with Session(engine) as session:
        for user in session.exec(select(User)).all():
            _notify_user(session, user, now)


def reschedule_scrape_job(minutes: int) -> None:
    scheduler.reschedule_job("scrape_job", trigger=IntervalTrigger(minutes=minutes))


def start_scheduler() -> None:
    with Session(engine) as session:
        interval = int(get_setting(session, "scrape_interval_minutes") or 60)

    scheduler.add_job(
        scrape_job,
        trigger=IntervalTrigger(minutes=interval),
        id="scrape_job",
        next_run_time=datetime.now(),
        max_instances=1,
        coalesce=True,
    )
    scheduler.add_job(
        notify_check_job,
        trigger=IntervalTrigger(minutes=1),
        id="notify_check_job",
        max_instances=1,
        coalesce=True,
    )
    scheduler.start()
