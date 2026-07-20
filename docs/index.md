# BaseAlert

Überwacht die Sendepläne von TechnoBase.FM, HouseTime.FM, HardBase.FM und
TranceBase.FM und schickt eine Benachrichtigung, wenn ein favorisierter DJ
auflegt – egal auf welchem der vier Sender. DJs werden global verfolgt, nicht
pro Sender. Mehrbenutzerfähig: jeder Account hat seine eigenen Favoriten und
seine eigenen Benachrichtigungskanäle.

Wähle links eine Setup-Variante, um loszulegen:

- **[Entwicklung](setup/development.md)** – lokal am Code arbeiten
- **[Linux](setup/linux.md)** – direkt auf einem Server als systemd-Dienst
- **[Docker](setup/docker.md)** – `docker compose up -d --build`
- **[Podman](setup/podman.md)** – rootless, inkl. Quadlet-Unit
- **[Kubernetes](setup/kubernetes.md)** – Beispiel-Manifest für k3s & Co.

Danach [Konfiguration](configuration.md) für die Referenz aller Umgebungsvariablen.

## Features

- **Dashboard** – "Jetzt auf Sendung" pro Sender, nächste Favoriten-Shows und
  eine nach Tag gruppierte Zeitleiste der kommenden 48 Stunden
- **DJs** – alle bisher gescrapten DJs (global, sender-übergreifend)
  durchsuchen, favorisieren und per "Nur Favoriten"-Filter auf die eigene
  Auswahl eingrenzen
- **Zeitfenster** – Wochentage + Uhrzeit anlegen (z.B. "Mo-Fr 15:00-17:00").
  Startet in so einem Fenster irgendeine Show, wird benachrichtigt – egal ob
  der DJ favorisiert ist. Praktisch für Zeiten, in denen dir jeder Live-DJ
  lieber ist als die automatische Playlist.
- **Einstellungen** – Scrape-Intervall ist global (gemeinsame Sendeplan-Daten
  für alle), Vorlaufzeit der Benachrichtigung sowie die Benachrichtigungskanäle
  sind pro Account:
    - **Pushover** (User Key + API Token von https://pushover.net)
    - **ntfy** (Server-URL + Topic, z.B. der öffentliche https://ntfy.sh oder
      eine eigene Instanz)
    - **Telegram** (Bot-Token + Chat-ID)
    - **Discord** (Webhook-URL eines Kanals)
    - **Generischer Webhook** (POST von `{title, message, url}` als JSON an
      eine beliebige URL)
    - **E-Mail** (SMTP-Zugangsdaten)

  Jeder Kanal hat einen eigenen "Testen"-Button, der mit dem zuletzt
  gespeicherten Stand dieses Kanals eine Testbenachrichtigung schickt. Eine
  Schritt-für-Schritt-Anleitung pro Kanal gibt's unter
  [Benachrichtigungskanäle](notifications.md).

Registrierung ist standardmäßig offen – jeder mit Zugriff auf die URL kann
sich einen Account anlegen. Sobald alle gewünschten Accounts existieren,
[`REGISTRATION_ENABLED=false`](configuration.md) setzen, um weitere
Registrierungen zu blockieren – bestehende Accounts können sich weiterhin
einloggen.

Beim ersten Start wird sofort einmal gescraped; danach läuft es automatisch im
eingestellten Intervall.

## Mehr

- **[FAQ / Troubleshooting](faq.md)** – häufige Stolpersteine
- **[Architektur](architecture.md)** – wie Scraping, Benachrichtigungen und
  Multi-Tenancy zusammenspielen
- **[Mitwirken](contributing.md)** – Branch/PR-Ablauf, Tests, Lint
- **[Sicherheitslücke melden](https://github.com/steven-streller/BaseAlert/security/policy)**
  – Meldeweg für Schwachstellen (siehe `SECURITY.md` im Repo)
