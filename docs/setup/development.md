# Entwicklung

```bash
git clone https://github.com/steven-streller/BaseAlert.git
cd BaseAlert
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements-dev.txt
```

## Tests & Lint

```bash
pytest              # 49 Tests, ~76% Coverage
ruff check .         # Lint
```

CI (`.github/workflows/ci.yml`) führt beides plus einen Docker-Build-Check bei
jedem Push/PR aus.

## Lokal starten

```bash
BASEALERT_DB_PATH=./data/basealert.db SESSION_SECRET_KEY=dev-only \
  uvicorn app.main:app --reload --port 8000
```

Die SQLite-Datenbank landet dann in `./data/basealert.db` (per `.gitignore`
ausgeschlossen). `--reload` startet den Server bei Codeänderungen neu –
Achtung: das reißt den laufenden APScheduler mit hoch, das ist für lokale
Entwicklung unproblematisch.

## Projektstruktur

```text
app/
  main.py            FastAPI-Routen (Auth, Dashboard, DJs, Zeitfenster, Settings)
  auth.py            Sessions, Passwort-Hashing-Dependency
  security.py        bcrypt hash/verify
  db.py              Engine, Migrations-freie Schema-Erstellung, Settings-Helper
  models.py          SQLModel-Tabellen
  scraper.py         HTML-Parsing + DB-Upsert der Sendepläne
  scheduler.py        APScheduler-Jobs (Scrape, Benachrichtigungs-Check)
  notifications.py   Kanal-Registry + Sendefunktionen (Pushover/ntfy/...)
  templates/         Jinja2 + HTMX
  static/            CSS, htmx.min.js
tests/               pytest, inkl. TestClient-basierte HTTP-Tests
docs/                diese Doku (MkDocs Material)
```

## Neuen Benachrichtigungskanal hinzufügen

Kanäle sind in `app/notifications.py` als Daten definiert (`CHANNELS`-Dict:
Label, Sendefunktion, Felder). Ein neuer Kanal braucht dort nur einen neuen
Eintrag – die Einstellungen-Seite rendert Formularfelder automatisch aus den
`fields`-Angaben, keine Template-Änderung nötig.
