import logging
from datetime import datetime, timedelta

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.interval import IntervalTrigger
from sqlmodel import Session, select

from app.db import engine, get_setting
from app.models import Dj, Favorite, NotificationLog, Show, Station
from app.notifications import enabled_channels, notify_all
from app.scraper import scrape_all

logger = logging.getLogger("basealert.scheduler")

scheduler = BackgroundScheduler()


def scrape_job() -> None:
    logger.info("Running scheduled scrape")
    results = scrape_all()
    logger.info("Scrape results: %s", results)


def notify_check_job() -> None:
    with Session(engine) as session:
        if not enabled_channels(session):
            return
        lead_minutes = int(get_setting(session, "notify_lead_minutes") or 15)

        now = datetime.now()
        window_end = now + timedelta(minutes=lead_minutes)

        favorite_dj_ids = set(session.exec(select(Favorite.dj_id)).all())
        if not favorite_dj_ids:
            return

        already_notified = set(session.exec(select(NotificationLog.show_id)).all())

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
            results = notify_all(session, title, message, url=station.base_url)
            if any(results.values()):
                session.add(NotificationLog(show_id=show.id))
                session.commit()
                logger.info(
                    "Notified for %s on %s at %s via %s",
                    dj.name,
                    station.key,
                    show.start_time,
                    [c for c, ok in results.items() if ok],
                )


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
