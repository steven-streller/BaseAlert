# Architektur

Ein Überblick für alle, die am Code mitarbeiten oder einfach verstehen
wollen, wie die Teile zusammenspielen – für den Betrieb selbst ist das nicht
nötig, siehe stattdessen die [Setup-Anleitungen](setup/development.md).

## Stack

FastAPI-App, serverseitig gerendertes HTML (Jinja2) mit HTMX für
Interaktivität ohne eigenes JavaScript-Build, SQLite über SQLModel
(SQLAlchemy), APScheduler für Hintergrund-Jobs im selben Prozess – ein
einziger Container, kein separater Worker/Broker.

## Datenmodell

| Tabelle | Umfang | Zweck |
|---|---|---|
| `Station` | global, fix (4 Sender) | Name, Basis-URL, Farbe |
| `Dj` | global | DJs werden **über alle Sender hinweg per Name** dedupliziert – derselbe DJ auf zwei Sendern ist eine Zeile |
| `Show` | global | Ein Sendeplan-Eintrag: Sender, DJ, Zeit, Show-Name, Genre |
| `User` | pro Account | E-Mail, bcrypt-Passworthash, `is_admin`-Flag |
| `Favorite` | pro Nutzer | Verknüpfung User↔Dj |
| `ListeningWindow` | pro Nutzer | Wochentage + Zeitfenster für "jede Show benachrichtigen" |
| `Setting` | global | Aktuell nur `scrape_interval_minutes` |
| `UserSetting` | pro Nutzer | Benachrichtigungskanäle + Vorlaufzeit |
| `NotificationLog` | pro Nutzer | Verhindert doppelte Benachrichtigungen für dieselbe Show |
| `ScrapeStatus` | global, pro Sender | Letzter Scrape-Versuch/-Erfolg, letzter Fehler, Fehler-Streak – Grundlage der Admin-Statusseite |

Warum DJs/Shows global, aber Favoriten/Kanäle pro Nutzer? Der Sendeplan ist
für alle identisch (dieselben vier Sender, dieselben Shows) – das einmal zu
scrapen und zu teilen spart unnötige Arbeit. Wer benachrichtigt werden will
und worüber ist dagegen inhärent persönlich.

## Scraping (`app/scraper.py`)

`scrape_job` läuft alle `scrape_interval_minutes` (Standard 60, global
einstellbar). Für jeden der vier Sender werden die Sendeplan-Seiten für heute
+ 6 Folgetage abgerufen (`?station=...&day=...`), die Server liefern dabei
serverseitig gerenderte HTML-Seiten mit
[schema.org `BroadcastEvent`-Microdata](https://schema.org/BroadcastEvent) –
`_parse_events` extrahiert daraus DJ, Show-Name, Genre, Start-/Endzeit ohne
Regex-Bastelei. Neue DJs/Shows werden angelegt, bestehende (gleicher Sender +
Startzeit) aktualisiert statt dupliziert.

## Benachrichtigungen (`app/scheduler.py`)

`notify_check_job` läuft jede Minute und iteriert über **alle** Nutzer
(`_notify_user`). Pro Nutzer:

1. Überspringen, wenn kein Kanal aktiviert ist oder weder Favoriten noch
   Zeitfenster existieren.
2. Alle Shows im Vorlaufzeit-Fenster (`start_time` zwischen jetzt und
   jetzt + `notify_lead_minutes`) laden.
3. Pro Show: passt der DJ zu einem Favoriten, **oder** fällt die Startzeit in
   eines der Zeitfenster (Wochentag + Uhrzeit-Abgleich, siehe
   `_show_in_any_window`)?
4. Noch nicht benachrichtigt (`NotificationLog`)? Dann über alle aktivierten
   Kanäle senden (`notify_all`) und den Log-Eintrag anlegen.

Die Dedup-Logik ist bewusst nicht "einmal pro Tag" sondern "einmal pro
(Nutzer, Show)" – jede einzelne Show wird genau einmal gemeldet, unabhängig
davon, wie oft der Scheduler in der Zwischenzeit läuft.

## Auth (`app/auth.py`, `app/security.py`)

Cookie-Session über Starlettes `SessionMiddleware` (mit `itsdangerous`
signiert, `SESSION_SECRET_KEY`), Passwörter mit `bcrypt` gehasht. Die
`require_user`-Dependency gated alle Routen außer `/login`, `/register` und
`/healthz` – bei fehlender Session ein 303-Redirect zu `/login` (inkl.
`HX-Redirect`-Header, damit HTMX-Requests nicht nur den betroffenen
Seitenausschnitt austauschen, sondern die ganze Seite neu laden).

## Benachrichtigungskanäle (`app/notifications.py`)

Kanäle sind als Daten definiert (`CHANNELS`-Dict: Label, Sendefunktion,
Formularfelder) statt als Code pro Kanal in den Templates. Die
Einstellungen-Seite rendert die Formulare generisch aus dieser Struktur – ein
neuer Kanal braucht dort nur einen neuen Eintrag, keine Template-Änderung.

## Admin (`/admin/users`, `/admin/health`)

Der erste registrierte Account wird beim `/register` automatisch zum Admin
(`is_admin=True`); alle danach registrierten Accounts starten ohne das Flag.
Weitere Admins lassen sich über die Nutzerverwaltung ernennen. Die
`require_admin`-Dependency (`app/auth.py`) gated beide Routen und antwortet
Nicht-Admins mit 404 statt 403, um die Existenz der Seiten nicht preiszugeben.

Ein Admin kann sich selbst weder demoten noch löschen (serverseitig
erzwungen, nicht nur im UI versteckt) – das verhindert einen Zustand ohne
verbleibenden Admin. Löschen eines Accounts entfernt dessen `Favorite`-,
`ListeningWindow`-, `UserSetting`- und `NotificationLog`-Zeilen mit, da SQLite
hier keine `ON DELETE CASCADE`-Fremdschlüssel hat.

Da `User.is_admin` eine neue Spalte auf einer bereits existierenden Tabelle
ist, reicht `SQLModel.metadata.create_all()` bei einem Upgrade eines
laufenden Installs nicht aus (das legt nur fehlende *Tabellen* an, keine
fehlenden Spalten). `init_db()` prüft deshalb per `PRAGMA table_info(user)`,
ob die Spalte fehlt, und holt sie einmalig per `ALTER TABLE` nach.

`ScrapeStatus` wird von `scrape_all()` (`app/scraper.py`) nach jedem
Scrape-Versuch pro Sender aktualisiert: bei Erfolg `last_success_at` und
Anzahl verarbeiteter Einträge, bei einer Exception `last_error` und ein
Fehler-Zähler, der bei jedem weiteren Fehlversuch hochzählt und beim
nächsten Erfolg zurückgesetzt wird.
