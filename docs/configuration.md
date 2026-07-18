# Konfiguration

Alle Einstellungen, die vor dem Start feststehen müssen, laufen über
Umgebungsvariablen. Alles, was sich zur Laufzeit pro Account ändert
(Benachrichtigungskanäle, Zeitfenster, Favoriten, Vorlaufzeit), läuft über die
Weboberfläche selbst (Einstellungen-Seite).

| Variable | Default | Beschreibung |
|---|---|---|
| `BASEALERT_DB_PATH` | `/app/data/basealert.db` | Pfad zur SQLite-Datenbank. |
| `SESSION_SECRET_KEY` | zufällig bei jedem Start | Signiert die Session-Cookies. Ohne festen Wert sind nach jedem Neustart alle abgemeldet. Generieren mit `python3 -c "import secrets; print(secrets.token_hex(32))"`. |
| `REGISTRATION_ENABLED` | `true` | Auf `false` setzen, sobald alle gewünschten Accounts existieren, um `/register` zu sperren. Bestehende Accounts können sich weiterhin einloggen. |
| `TZ` | (System-Default) | Zeitzone für Sendezeiten und Benachrichtigungs-Zeitpunkte. Sollte auf `Europe/Berlin` stehen, sonst weichen die angezeigten/verglichenen Uhrzeiten vom tatsächlichen Sendeplan ab. |

## Scrape-Intervall & Vorlaufzeit

`Scrape-Intervall` (wie oft die vier Sender neu abgefragt werden) ist global
für alle Accounts gemeinsam, da die Sendeplan-Daten selbst geteilt sind – nicht
über eine Umgebungsvariable, sondern über die Einstellungen-Seite (Standard: 60
Minuten).

`Vorlaufzeit der Benachrichtigung` (wie viele Minuten vor Show-Start
benachrichtigt wird) ist dagegen pro Account einstellbar, ebenfalls über die
Einstellungen-Seite (Standard: 15 Minuten).

## Benachrichtigungskanäle

Pushover, ntfy, Telegram, Discord, generischer Webhook und E-Mail werden
komplett über die Einstellungen-Seite pro Account konfiguriert, nicht über
Umgebungsvariablen – siehe [Start](index.md#features).
