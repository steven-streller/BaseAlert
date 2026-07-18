import logging
from datetime import datetime, timedelta

from fastapi import FastAPI, Request
from fastapi.responses import RedirectResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from sqlmodel import Session, select

from app.db import engine, get_setting, init_db, set_setting
from app.models import Dj, Favorite, Show, Station
from app.notifications import CHANNELS, send_to_channel
from app.scheduler import reschedule_scrape_job, start_scheduler
from app.scraper import scrape_all

logging.basicConfig(level=logging.INFO)

app = FastAPI(title="BaseAlert")
app.mount("/static", StaticFiles(directory="app/static"), name="static")
templates = Jinja2Templates(directory="app/templates")


@app.on_event("startup")
def on_startup() -> None:
    init_db()
    start_scheduler()


WEEKDAYS_DE = ["Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"]


def _day_label(day, today) -> str:
    delta = (day - today).days
    if delta == 0:
        return "Heute"
    if delta == 1:
        return "Morgen"
    return WEEKDAYS_DE[day.weekday()]


def _enrich(session: Session, shows: list[Show], favorite_dj_ids: set[int], today=None) -> list[dict]:
    dj_ids = {s.dj_id for s in shows if s.dj_id}
    station_ids = {s.station_id for s in shows}
    djs = {d.id: d for d in session.exec(select(Dj).where(Dj.id.in_(dj_ids))).all()} if dj_ids else {}
    stations = {s.id: s for s in session.exec(select(Station).where(Station.id.in_(station_ids))).all()}
    rows = []
    for show in shows:
        row = {
            "show": show,
            "dj": djs.get(show.dj_id),
            "station": stations.get(show.station_id),
            "is_favorite": show.dj_id in favorite_dj_ids,
        }
        if today is not None:
            row["day_label"] = _day_label(show.start_time.date(), today)
        rows.append(row)
    return rows


@app.get("/")
def dashboard(request: Request):
    with Session(engine) as session:
        now = datetime.now()
        today = now.date()
        favorite_dj_ids = set(session.exec(select(Favorite.dj_id)).all())

        favorite_rows = []
        if favorite_dj_ids:
            favorite_shows = session.exec(
                select(Show)
                .where(Show.start_time >= now, Show.dj_id.in_(favorite_dj_ids))
                .order_by(Show.start_time)
                .limit(10)
            ).all()
            favorite_rows = _enrich(session, favorite_shows, favorite_dj_ids)

        stations = session.exec(select(Station)).all()
        now_playing = []
        for station in stations:
            current = session.exec(
                select(Show)
                .where(Show.station_id == station.id, Show.start_time <= now)
                .order_by(Show.start_time.desc())
                .limit(1)
            ).first()
            if current and (current.end_time is None or current.end_time > now):
                row = _enrich(session, [current], favorite_dj_ids)[0]
            else:
                row = {"show": None, "dj": None, "station": station, "is_favorite": False}
            now_playing.append(row)

        upcoming_shows = session.exec(
            select(Show)
            .where(Show.start_time >= now, Show.start_time <= now + timedelta(hours=48))
            .order_by(Show.start_time)
        ).all()
        upcoming_rows = _enrich(session, upcoming_shows, favorite_dj_ids, today=today)

        day_groups = []
        for row in upcoming_rows:
            if not day_groups or day_groups[-1][0] != row["day_label"]:
                day_groups.append((row["day_label"], []))
            day_groups[-1][1].append(row)

    return templates.TemplateResponse(
        "dashboard.html",
        {
            "request": request,
            "active": "dashboard",
            "favorite_rows": favorite_rows,
            "now_playing": now_playing,
            "day_groups": day_groups,
        },
    )


def _dj_station_map(session: Session) -> dict[int, list[Station]]:
    """Which stations each DJ has been seen playing on, regardless of favorite status."""
    stations = {s.id: s for s in session.exec(select(Station)).all()}
    pairs = session.exec(select(Show.dj_id, Show.station_id).distinct()).all()
    mapping: dict[int, list[Station]] = {}
    for dj_id, station_id in pairs:
        if dj_id is None or station_id not in stations:
            continue
        mapping.setdefault(dj_id, []).append(stations[station_id])
    for station_list in mapping.values():
        station_list.sort(key=lambda s: s.name)
    return mapping


def _dj_rows(session: Session, q: str) -> list[dict]:
    query = select(Dj)
    if q:
        query = query.where(Dj.name.ilike(f"%{q}%"))
    djs = sorted(session.exec(query).all(), key=lambda d: d.name.lower())
    station_map = _dj_station_map(session)
    favorite_ids = set(session.exec(select(Favorite.dj_id)).all())
    return [
        {
            "dj": dj,
            "stations": station_map.get(dj.id, []),
            "is_favorite": dj.id in favorite_ids,
        }
        for dj in djs
    ]


@app.get("/djs")
def djs_page(request: Request, q: str = ""):
    with Session(engine) as session:
        rows = _dj_rows(session, q)
    return templates.TemplateResponse(
        "djs.html", {"request": request, "active": "djs", "rows": rows, "q": q}
    )


@app.get("/djs/list")
def djs_list(request: Request, q: str = ""):
    with Session(engine) as session:
        rows = _dj_rows(session, q)
    return templates.TemplateResponse("_dj_list.html", {"request": request, "rows": rows})


@app.post("/djs/{dj_id}/toggle")
def toggle_favorite(request: Request, dj_id: int):
    with Session(engine) as session:
        existing = session.exec(select(Favorite).where(Favorite.dj_id == dj_id)).first()
        if existing:
            session.delete(existing)
            session.commit()
            is_favorite = False
        else:
            session.add(Favorite(dj_id=dj_id))
            session.commit()
            is_favorite = True
        dj = session.get(Dj, dj_id)
        stations = _dj_station_map(session).get(dj_id, [])
    return templates.TemplateResponse(
        "_dj_row.html",
        {"request": request, "dj": dj, "stations": stations, "is_favorite": is_favorite},
    )


# checkbox settings keys that are absent from form data when unchecked
CHANNEL_CHECKBOX_FIELDS = {
    field[0] for channel in CHANNELS.values() for field in channel["fields"] if field[2] == "checkbox"
}
CHECKBOX_KEYS = list(CHANNEL_CHECKBOX_FIELDS) + [f"{c}_enabled" for c in CHANNELS]
GENERAL_KEYS = ["scrape_interval_minutes", "notify_lead_minutes"]
CHANNEL_TEXT_KEYS = [key for channel in CHANNELS.values() for key in channel["keys"]]


@app.get("/settings")
def settings_page(request: Request, saved: str = "", tested: str = "", scraped: str = ""):
    with Session(engine) as session:
        keys = GENERAL_KEYS + CHECKBOX_KEYS + CHANNEL_TEXT_KEYS
        settings = {key: get_setting(session, key) for key in keys}

    flash = None
    if saved:
        label = CHANNELS[saved]["label"] if saved in CHANNELS else "Allgemein"
        flash = f"„{label}“ gespeichert."
    elif tested == "ok":
        flash = "Test-Benachrichtigung gesendet."
    elif tested == "fail":
        flash = "Test-Benachrichtigung fehlgeschlagen – prüfe die Zugangsdaten und die Logs."
    elif scraped:
        flash = "Sendepläne wurden aktualisiert."

    return templates.TemplateResponse(
        "settings.html",
        {
            "request": request,
            "active": "settings",
            "settings": settings,
            "channels": CHANNELS,
            "flash": flash,
        },
    )


@app.post("/settings")
async def save_settings(request: Request):
    form = await request.form()
    section = form.get("_section", "general")

    with Session(engine) as session:
        if section == "general":
            scrape_interval = max(5, int(form.get("scrape_interval_minutes") or 60))
            notify_lead = max(1, int(form.get("notify_lead_minutes") or 15))
            set_setting(session, "scrape_interval_minutes", str(scrape_interval))
            set_setting(session, "notify_lead_minutes", str(notify_lead))
            reschedule_scrape_job(scrape_interval)
        elif section in CHANNELS:
            set_setting(session, f"{section}_enabled", "true" if form.get(f"{section}_enabled") else "false")
            for key in CHANNELS[section]["keys"]:
                if key in CHANNEL_CHECKBOX_FIELDS:
                    set_setting(session, key, "true" if form.get(key) else "false")
                else:
                    set_setting(session, key, str(form.get(key, "")).strip())

    return RedirectResponse(url=f"/settings?saved={section}#{section}", status_code=303)


@app.post("/settings/test/{channel}")
def test_notification(channel: str):
    if channel not in CHANNELS:
        return RedirectResponse(url="/settings?tested=fail", status_code=303)
    with Session(engine) as session:
        ok = send_to_channel(
            session, channel, "BaseAlert Test", "Testbenachrichtigung von BaseAlert 🎧"
        )
    return RedirectResponse(url=f"/settings?tested={'ok' if ok else 'fail'}#{channel}", status_code=303)


@app.post("/scrape-now")
def scrape_now():
    scrape_all()
    return RedirectResponse(url="/settings?scraped=1", status_code=303)
