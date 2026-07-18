# FAQ / Troubleshooting

## Ich bekomme keine Benachrichtigungen

Der Reihe nach prüfen:

1. Ist mindestens ein Kanal in den **Einstellungen** aktiviert (Häkchen bei
   "Aktiviert", nicht nur ausgefüllte Felder)? Speichern nicht vergessen.
2. Funktioniert der Kanal über den **"Testen"-Button** der jeweiligen Sektion?
   Wenn nicht: Zugangsdaten prüfen, Logs ansehen (`docker compose logs` bzw.
   `journalctl -u basealert`).
3. Ist der DJ favorisiert **oder** fällt die Show in eines deiner
   [Zeitfenster](index.md#features)?
4. Liegt die Show-Startzeit innerhalb der eingestellten Vorlaufzeit
   (Einstellungen → "Vorlaufzeit der Benachrichtigung")? Bei 15 Minuten
   Vorlauf kommt die Nachricht erst 15 Minuten vor Sendestart, nicht früher.
5. Wurde für diese Show schon einmal benachrichtigt? Pro Show gibt es genau
   eine Benachrichtigung pro Account (kein Spam bei jedem Scheduler-Tick).

## "I have no name!" beim Exec in den Container

Der Container läuft als `appuser` (UID/GID 1000), nicht als root. Zeigt eine
interaktive Shell trotzdem "I have no name!", erzwingt die Umgebung (z.B.
Kubernetes `securityContext.runAsUser`) eine andere UID als 1000, für die es
keinen `/etc/passwd`-Eintrag im Image gibt. Siehe
[Kubernetes-Setup](setup/kubernetes.md#non-root) für den nötigen
`securityContext` (inkl. `fsGroup`, damit die Datenbank auch beschreibbar
bleibt).

## Nach jedem Neustart bin ich abgemeldet

`SESSION_SECRET_KEY` ist nicht gesetzt – ohne festen Wert generiert BaseAlert
bei jedem Start einen neuen zufälligen Schlüssel, wodurch alle bestehenden
Sessions ungültig werden. Siehe [Konfiguration](configuration.md).

## Ein Sender liefert im Log eine DNS-/Verbindungsfehlermeldung

```
WARNING:basealert.scraper:Failed to fetch hardbase.fm day ...: HTTPSConnectionPool(...)
```

Das ist meist eine vorübergehende Netzwerk-Störung (DNS-Auflösung o.ä.) beim
Scrapen dieses einen Senders an diesem einen Tag – die anderen drei Sender
und die übrigen Tage sind davon nicht betroffen, und der nächste
Scrape-Durchlauf holt es normalerweise nach. Tritt es dauerhaft nur für einen
Sender auf, prüfen ob der Container/die Node überhaupt DNS-Auflösung nach
außen hat.

## Uhrzeiten im Sendeplan oder in Benachrichtigungen stimmen nicht

`TZ` ist nicht auf `Europe/Berlin` gesetzt. Sendezeiten werden von den
Sender-Webseiten als lokale (deutsche) Zeit interpretiert – läuft der
Container in einer anderen Zeitzone, verschieben sich sowohl die Anzeige als
auch der Abgleich "startet die Show gleich" entsprechend.

## Registrierung geht nicht mehr

`REGISTRATION_ENABLED=false` gesetzt (absichtlich, siehe
[Konfiguration](configuration.md)) – bestehende Accounts können sich
weiterhin einloggen, es kann sich nur niemand Neues mehr registrieren.

## Backup

Die komplette Datenbank ist eine einzelne SQLite-Datei
(`BASEALERT_DB_PATH`, standardmäßig `/app/data/basealert.db`). Ein Backup ist
einfach eine Kopie dieser Datei (Container vorher stoppen oder zumindest
kurz pausieren, um mitten in einem Schreibvorgang eine Kopie zu vermeiden).
Wiederherstellen: Datei an die gleiche Stelle zurückkopieren und den
Container neu starten.
