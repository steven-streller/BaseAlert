import logging
import os
import secrets
from datetime import datetime, time, timedelta

from fastapi import Depends, FastAPI, Request
from fastapi.responses import RedirectResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from sqlmodel import Session, select
from starlette.middleware.sessions import SessionMiddleware

from app.auth import get_current_user, login_user, logout_user, require_user
from app.db import engine, get_setting, get_user_setting, init_db, set_setting, set_user_setting
from app.models import Dj, Favorite, ListeningWindow, Show, Station, User
from app.notifications import CHANNELS, send_to_channel
from app.scheduler import reschedule_scrape_job, start_scheduler
from app.scraper import scrape_all
from app.security import hash_password, verify_password

logging.basicConfig(level=logging.INFO)


class _HealthCheckLogFilter(logging.Filter):
    """Keeps k8s readiness/liveness probe hits out of the access log - they'd
    otherwise drown out everything else every few seconds."""

    def filter(self, record: logging.LogRecord) -> bool:
        return "/healthz" not in record.getMessage()


logging.getLogger("uvicorn.access").addFilter(_HealthCheckLogFilter())

# APScheduler logs "Running job ..." / "... executed successfully" at INFO
# for every single run - with notify_check_job firing every minute that's
# two log lines a minute forever. Failures still come through at ERROR.
logging.getLogger("apscheduler.executors.default").setLevel(logging.WARNING)

app = FastAPI(title="BaseAlert")
app.add_middleware(
    SessionMiddleware,
    secret_key=os.environ.get("SESSION_SECRET_KEY") or secrets.token_hex(32),
    same_site="lax",
)
app.mount("/static", StaticFiles(directory="app/static"), name="static")
templates = Jinja2Templates(directory="app/templates")

REGISTRATION_ENABLED = os.environ.get("REGISTRATION_ENABLED", "true").lower() != "false"


@app.on_event("startup")
def on_startup() -> None:
    init_db()
    start_scheduler()


@app.get("/healthz")
def healthz():
    return {"status": "ok"}


# --- Auth -------------------------------------------------------------------


@app.get("/register")
def register_page(request: Request, error: str = ""):
    if get_current_user(request):
        return RedirectResponse(url="/", status_code=303)
    return templates.TemplateResponse(
        request,
        "register.html",
        {"error": error, "registration_enabled": REGISTRATION_ENABLED},
    )


@app.post("/register")
async def register(request: Request):
    if not REGISTRATION_ENABLED:
        return RedirectResponse(url="/register", status_code=303)

    form = await request.form()
    email = str(form.get("email", "")).strip().lower()
    password = str(form.get("password", ""))
    password_confirm = str(form.get("password_confirm", ""))

    if not email or "@" not in email:
        return RedirectResponse(url="/register?error=email", status_code=303)
    if len(password) < 8:
        return RedirectResponse(url="/register?error=password_length", status_code=303)
    if password != password_confirm:
        return RedirectResponse(url="/register?error=password_mismatch", status_code=303)

    with Session(engine) as session:
        if session.exec(select(User).where(User.email == email)).first():
            return RedirectResponse(url="/register?error=taken", status_code=303)
        user = User(email=email, password_hash=hash_password(password))
        session.add(user)
        session.commit()
        session.refresh(user)

    login_user(request, user)
    return RedirectResponse(url="/", status_code=303)


@app.get("/login")
def login_page(request: Request, error: str = ""):
    if get_current_user(request):
        return RedirectResponse(url="/", status_code=303)
    return templates.TemplateResponse(
        request,
        "login.html",
        {"error": error, "registration_enabled": REGISTRATION_ENABLED},
    )


@app.post("/login")
async def login(request: Request):
    form = await request.form()
    email = str(form.get("email", "")).strip().lower()
    password = str(form.get("password", ""))

    with Session(engine) as session:
        user = session.exec(select(User).where(User.email == email)).first()

    if not user or not verify_password(password, user.password_hash):
        return RedirectResponse(url="/login?error=1", status_code=303)

    login_user(request, user)
    return RedirectResponse(url="/", status_code=303)


@app.post("/logout")
def logout(request: Request):
    logout_user(request)
    return RedirectResponse(url="/login", status_code=303)


# --- Dashboard ----------------------------------------------------------------

WEEKDAYS_DE = ["Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"]
WEEKDAY_SHORT_LABELS = ["Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"]


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
            "weekday_short": WEEKDAY_SHORT_LABELS[show.start_time.weekday()],
        }
        if today is not None:
            row["day_label"] = _day_label(show.start_time.date(), today)
        rows.append(row)
    return rows


@app.get("/")
def dashboard(request: Request, current_user: User = Depends(require_user)):
    with Session(engine) as session:
        now = datetime.now()
        today = now.date()
        favorite_dj_ids = set(
            session.exec(select(Favorite.dj_id).where(Favorite.user_id == current_user.id)).all()
        )

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
        request,
        "dashboard.html",
        {
            "active": "dashboard",
            "current_user": current_user,
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


def _dj_rows(session: Session, user_id: int, q: str, favorites_only: bool = False) -> list[dict]:
    query = select(Dj)
    if q:
        query = query.where(Dj.name.ilike(f"%{q}%"))
    djs = sorted(session.exec(query).all(), key=lambda d: d.name.lower())
    station_map = _dj_station_map(session)
    favorite_ids = set(session.exec(select(Favorite.dj_id).where(Favorite.user_id == user_id)).all())
    if favorites_only:
        djs = [dj for dj in djs if dj.id in favorite_ids]
    return [
        {
            "dj": dj,
            "stations": station_map.get(dj.id, []),
            "is_favorite": dj.id in favorite_ids,
        }
        for dj in djs
    ]


@app.get("/djs")
def djs_page(
    request: Request, q: str = "", favorites_only: bool = False, current_user: User = Depends(require_user)
):
    with Session(engine) as session:
        rows = _dj_rows(session, current_user.id, q, favorites_only)
    return templates.TemplateResponse(
        request,
        "djs.html",
        {
            "active": "djs",
            "current_user": current_user,
            "rows": rows,
            "q": q,
            "favorites_only": favorites_only,
        },
    )


@app.get("/djs/list")
def djs_list(
    request: Request, q: str = "", favorites_only: bool = False, current_user: User = Depends(require_user)
):
    with Session(engine) as session:
        rows = _dj_rows(session, current_user.id, q, favorites_only)
    return templates.TemplateResponse(request, "_dj_list.html", {"rows": rows})


@app.post("/djs/{dj_id}/toggle")
def toggle_favorite(request: Request, dj_id: int, current_user: User = Depends(require_user)):
    with Session(engine) as session:
        existing = session.exec(
            select(Favorite).where(Favorite.user_id == current_user.id, Favorite.dj_id == dj_id)
        ).first()
        if existing:
            session.delete(existing)
            session.commit()
            is_favorite = False
        else:
            session.add(Favorite(user_id=current_user.id, dj_id=dj_id))
            session.commit()
            is_favorite = True
        dj = session.get(Dj, dj_id)
        stations = _dj_station_map(session).get(dj_id, [])
    return templates.TemplateResponse(
        request,
        "_dj_row.html",
        {"dj": dj, "stations": stations, "is_favorite": is_favorite},
    )


# --- Listening windows ---------------------------------------------------------


@app.get("/windows")
def windows_page(request: Request, saved: str = "", current_user: User = Depends(require_user)):
    with Session(engine) as session:
        listening_windows = session.exec(
            select(ListeningWindow).where(ListeningWindow.user_id == current_user.id)
        ).all()
    return templates.TemplateResponse(
        request,
        "windows.html",
        {
            "active": "windows",
            "current_user": current_user,
            "listening_windows": listening_windows,
            "weekday_labels": WEEKDAY_SHORT_LABELS,
            "flash": "Zeitfenster aktualisiert." if saved else None,
        },
    )


@app.post("/windows")
async def create_listening_window(request: Request, current_user: User = Depends(require_user)):
    form = await request.form()
    weekdays = form.getlist("weekdays")
    start_raw = str(form.get("start_time", ""))
    end_raw = str(form.get("end_time", ""))
    label = str(form.get("label", "")).strip() or None

    if not weekdays or not start_raw or not end_raw:
        return RedirectResponse(url="/windows", status_code=303)

    start_time = time.fromisoformat(start_raw)
    end_time = time.fromisoformat(end_raw)
    if start_time >= end_time:
        return RedirectResponse(url="/windows", status_code=303)

    with Session(engine) as session:
        session.add(
            ListeningWindow(
                user_id=current_user.id,
                label=label,
                weekdays=",".join(sorted(weekdays)),
                start_time=start_time,
                end_time=end_time,
            )
        )
        session.commit()

    return RedirectResponse(url="/windows?saved=1", status_code=303)


@app.post("/windows/{window_id}/delete")
def delete_listening_window(window_id: int, current_user: User = Depends(require_user)):
    with Session(engine) as session:
        window = session.get(ListeningWindow, window_id)
        if window and window.user_id == current_user.id:
            session.delete(window)
            session.commit()
    return RedirectResponse(url="/windows?saved=1", status_code=303)


# --- Settings -----------------------------------------------------------------

# checkbox settings keys that are absent from form data when unchecked
CHANNEL_CHECKBOX_FIELDS = {
    field[0] for channel in CHANNELS.values() for field in channel["fields"] if field[2] == "checkbox"
}
CHANNEL_TEXT_KEYS = [key for channel in CHANNELS.values() for key in channel["keys"]]

ALLOWED_SETTINGS_ANCHORS = ("general", *CHANNELS)


def _safe_settings_anchor(value: str) -> str:
    """Map arbitrary input onto a known-safe literal for use in a redirect URL/anchor.

    Returns one of the ALLOWED_SETTINGS_ANCHORS literals, never the input itself,
    so the redirect target can't carry attacker-controlled data (CWE-601).
    """
    for allowed in ALLOWED_SETTINGS_ANCHORS:
        if allowed == value:
            return allowed
    return "general"


@app.get("/settings")
def settings_page(
    request: Request,
    saved: str = "",
    tested: str = "",
    scraped: str = "",
    current_user: User = Depends(require_user),
):
    with Session(engine) as session:
        settings = {"scrape_interval_minutes": get_setting(session, "scrape_interval_minutes")}
        settings["notify_lead_minutes"] = get_user_setting(session, current_user.id, "notify_lead_minutes")
        for key in list(CHANNEL_CHECKBOX_FIELDS) + [f"{c}_enabled" for c in CHANNELS] + CHANNEL_TEXT_KEYS:
            settings[key] = get_user_setting(session, current_user.id, key)

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
        request,
        "settings.html",
        {
            "active": "settings",
            "current_user": current_user,
            "settings": settings,
            "channels": CHANNELS,
            "flash": flash,
        },
    )


@app.post("/settings")
async def save_settings(request: Request, current_user: User = Depends(require_user)):
    form = await request.form()
    section = form.get("_section", "general")

    with Session(engine) as session:
        if section == "general":
            scrape_interval = max(5, int(form.get("scrape_interval_minutes") or 60))
            notify_lead = max(1, int(form.get("notify_lead_minutes") or 15))
            set_setting(session, "scrape_interval_minutes", str(scrape_interval))
            set_user_setting(session, current_user.id, "notify_lead_minutes", str(notify_lead))
            reschedule_scrape_job(scrape_interval)
        elif section in CHANNELS:
            set_user_setting(
                session, current_user.id, f"{section}_enabled", "true" if form.get(f"{section}_enabled") else "false"
            )
            for key in CHANNELS[section]["keys"]:
                if key in CHANNEL_CHECKBOX_FIELDS:
                    set_user_setting(session, current_user.id, key, "true" if form.get(key) else "false")
                else:
                    set_user_setting(session, current_user.id, key, str(form.get(key, "")).strip())

    anchor = _safe_settings_anchor(section)
    return RedirectResponse(url=f"/settings?saved={anchor}#{anchor}", status_code=303)


@app.post("/settings/test/{channel}")
def test_notification(channel: str, current_user: User = Depends(require_user)):
    if channel not in CHANNELS:
        return RedirectResponse(url="/settings?tested=fail", status_code=303)
    with Session(engine) as session:
        ok = send_to_channel(
            session, current_user.id, channel, "BaseAlert Test", "Testbenachrichtigung von BaseAlert 🎧"
        )
    anchor = _safe_settings_anchor(channel)
    return RedirectResponse(url=f"/settings?tested={'ok' if ok else 'fail'}#{anchor}", status_code=303)


@app.post("/scrape-now")
def scrape_now(current_user: User = Depends(require_user)):
    scrape_all()
    return RedirectResponse(url="/settings?scraped=1", status_code=303)
