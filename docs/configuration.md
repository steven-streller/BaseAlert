# Konfiguration

Alle Einstellungen, die vor dem Start feststehen müssen, laufen über
Umgebungsvariablen. Alles, was sich zur Laufzeit pro Account ändert
(Benachrichtigungskanäle, Zeitfenster, Favoriten, Vorlaufzeit), läuft über die
Weboberfläche selbst (Einstellungen-Seite).

| Variable | Default | Beschreibung |
|---|---|---|
| `BASEALERT_DB_PATH` | `/app/data/basealert.db` | Pfad zur SQLite-Datenbank. |
| `SESSION_SECRET_KEY` | automatisch erzeugt & in der DB gespeichert | Signiert die Session-Cookies. Ohne gesetzte Variable erzeugt BaseAlert beim ersten Start einen zufälligen Wert und speichert ihn in der Datenbank, sodass Sessions Neustarts überleben – im Admin-Panel (`/admin/settings`) neu erzeugbar (meldet alle Nutzer ab). Nur nötig, wenn mehrere Instanzen (z. B. Kubernetes-Replicas) sich denselben Schlüssel teilen müssen sollen – ist die Variable gesetzt, hat sie immer Vorrang vor dem DB-Wert und ist im Admin-Panel nicht änderbar. Generieren mit `python3 -c "import secrets; print(secrets.token_hex(32))"`. |
| `REGISTRATION_ENABLED` | `true` | Nur als Startwert relevant: legt fest, ob `/register` beim allerersten Start gesperrt ist. Danach läuft die Umschaltung live über das Admin-Panel (`/admin/settings`), ganz ohne Neustart – die Umgebungsvariable wird ab dann ignoriert. Bestehende Accounts können sich unabhängig davon immer einloggen. |
| `TZ` | (System-Default) | Zeitzone für Sendezeiten und Benachrichtigungs-Zeitpunkte. Sollte auf `Europe/Berlin` stehen, sonst weichen die angezeigten/verglichenen Uhrzeiten vom tatsächlichen Sendeplan ab. |

## Scrape-Intervall & Vorlaufzeit

`Scrape-Intervall` (wie oft die vier Sender neu abgefragt werden) ist global
für alle Accounts gemeinsam, da die Sendeplan-Daten selbst geteilt sind – nicht
über eine Umgebungsvariable, sondern nur von einem Admin über
`/admin/health` einstellbar (Standard: 60 Minuten).

`Vorlaufzeit der Benachrichtigung` (wie viele Minuten vor Show-Start
benachrichtigt wird) ist dagegen pro Account einstellbar, ebenfalls über die
Einstellungen-Seite (Standard: 15 Minuten).

## Benachrichtigungskanäle

Pushover, ntfy, Telegram, Discord, generischer Webhook und E-Mail werden
komplett über die Einstellungen-Seite pro Account konfiguriert, nicht über
Umgebungsvariablen – siehe [Start](index.md#features).
