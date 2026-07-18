# BaseAlert

![CI](https://github.com/steven-streller/BaseAlert/actions/workflows/ci.yml/badge.svg)
![Docker Publish](https://github.com/steven-streller/BaseAlert/actions/workflows/docker-publish.yml/badge.svg)
![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)

Überwacht die Sendepläne von TechnoBase.FM, HouseTime.FM, HardBase.FM und TranceBase.FM
und schickt eine Benachrichtigung, wenn ein favorisierter DJ auflegt – egal auf welchem
der vier Sender. DJs werden global verfolgt, nicht pro Sender. Mehrbenutzerfähig: jeder
Account hat seine eigenen Favoriten und seine eigenen Benachrichtigungskanäle.

## Start (Docker Compose)

```bash
docker compose up -d --build
```

Danach unter http://localhost:8000 einen Account anlegen (`/register`) und einloggen.
Registrierung ist standardmäßig offen – jeder mit Zugriff auf die URL kann sich einen
Account anlegen. Sobald alle gewünschten Accounts existieren, `REGISTRATION_ENABLED=false`
setzen (`.env` bei Compose, Env-Var in [k8s/deployment.yaml](k8s/deployment.yaml)), um
weitere Registrierungen zu blockieren – bestehende Accounts können sich weiterhin einloggen.

- **Dashboard** – "Jetzt auf Sendung" pro Sender, nächste Favoriten-Shows und eine
  nach Tag gruppierte Zeitleiste der kommenden 48 Stunden
- **DJs** – alle bisher gescrapten DJs (global, sender-übergreifend) durchsuchen und favorisieren
- **Zeitfenster** – Wochentage + Uhrzeit anlegen (z.B. "Mo-Fr 15:00-17:00"). Startet in
  so einem Fenster irgendeine Show, wird benachrichtigt – egal ob der DJ favorisiert
  ist. Praktisch für Zeiten, in denen dir jeder Live-DJ lieber ist als die
  automatische Playlist.
- **Einstellungen** – Scrape-Intervall ist global (gemeinsame Sendeplan-Daten für alle),
  Vorlaufzeit der Benachrichtigung sowie die Benachrichtigungskanäle sind pro Account:
  - **Pushover** (User Key + API Token von https://pushover.net)
  - **ntfy** (Server-URL + Topic, z.B. der öffentliche https://ntfy.sh oder eine eigene Instanz)
  - **Telegram** (Bot-Token + Chat-ID)
  - **Discord** (Webhook-URL eines Kanals)
  - **Generischer Webhook** (POST von `{title, message, url}` als JSON an eine beliebige URL)
  - **E-Mail** (SMTP-Zugangsdaten)

  Jeder Kanal hat einen eigenen "Testen"-Button, der mit dem zuletzt gespeicherten
  Stand dieses Kanals eine Testbenachrichtigung schickt.

Beim ersten Start wird sofort einmal gescraped; danach läuft es automatisch im
eingestellten Intervall. Die SQLite-Datenbank liegt in `./data/basealert.db`.

### Session-Secret

Sessions werden über ein signiertes Cookie gehalten. Ohne `SESSION_SECRET_KEY` wird bei
jedem Container-Start ein zufälliger Schlüssel generiert – dann sind nach jedem Neustart
alle abgemeldet. Für einen stabilen Schlüssel `.env.example` nach `.env` kopieren und
befüllen:

```bash
cp .env.example .env
python3 -c "import secrets; print(secrets.token_hex(32))"  # in .env eintragen
```

## Deployment in Kubernetes / k3s

Fertige Manifeste liegen in [k8s/](k8s/). Sie erwarten das per CI gebaute Image
`ghcr.io/steven-streller/basealert:latest`.

```bash
kubectl apply -k k8s/
```

Das legt Namespace `basealert`, eine `PersistentVolumeClaim` (Storage-Class
`local-path`, wie sie k3s standardmäßig mitbringt), Deployment und Service an.
Ressourcen-Bedarf ist minimal (siehe [k8s/deployment.yaml](k8s/deployment.yaml)):
im Leerlauf ~70 MB RAM, kurze CPU-Spitzen nur während eines Scrape-Laufs.

- **Privates GHCR-Image**: Falls das Package nicht öffentlich ist, vorher ein
  Pull-Secret anlegen und in [k8s/deployment.yaml](k8s/deployment.yaml) das
  auskommentierte `imagePullSecrets` aktivieren:
  ```bash
  kubectl create secret docker-registry ghcr-creds -n basealert \
    --docker-server=ghcr.io --docker-username=<gh-user> --docker-password=<gh-pat>
  ```
- **Erreichbarkeit**: standardmäßig nur `ClusterIP`. Für Zugriff von außen entweder
  `kubectl port-forward -n basealert svc/basealert 8000:8000` nutzen, oder
  [k8s/ingress.yaml](k8s/ingress.yaml) mit echtem Hostnamen ausfüllen und separat
  anwenden (`kubectl apply -f k8s/ingress.yaml`) – k3s bringt dafür Traefik mit.
- **Non-root**: Das Image läuft als `appuser` (UID/GID 1000), passend zu
  `securityContext.runAsUser: 1000` + `fsGroup: 1000` in
  [k8s/deployment.yaml](k8s/deployment.yaml). Bei einem eigenen Deployment-Manifest
  (nicht über `kubectl apply -k k8s/`) muss `fsGroup: 1000` dort ebenfalls gesetzt
  sein, sonst ist die PVC nicht beschreibbar bzw. zeigt UID 1000 als
  "I have no name!" ohne passenden `/etc/passwd`-Eintrag.

## Entwicklung

```bash
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements-dev.txt

pytest              # Tests
ruff check .        # Lint
uvicorn app.main:app --reload --port 8000
```

CI (`.github/workflows/ci.yml`) führt Lint, Tests und einen Docker-Build-Check bei
jedem Push/PR aus. Auf `main` und bei `v*.*.*`-Tags baut und veröffentlicht
`.github/workflows/docker-publish.yml` zusätzlich ein Multi-Arch-Image
(`linux/amd64` + `linux/arm64`) nach GHCR.
