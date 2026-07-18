# Docker

```bash
git clone https://github.com/steven-streller/BaseAlert.git
cd BaseAlert
docker compose up -d --build
```

Danach unter <http://localhost:8000> einen Account anlegen (`/register`) und
einloggen.

## `docker-compose.yml`

```yaml
services:
  basealert:
    build: .
    container_name: basealert
    restart: unless-stopped
    ports:
      - "8000:8000"
    volumes:
      - ./data:/app/data
    environment:
      - TZ=Europe/Berlin
      - SESSION_SECRET_KEY=${SESSION_SECRET_KEY:-}
      - REGISTRATION_ENABLED=${REGISTRATION_ENABLED:-true}
```

Die SQLite-Datenbank liegt in `./data/basealert.db` (Bind-Mount, überlebt
`docker compose down`).

## Session-Secret

Ohne `SESSION_SECRET_KEY` wird bei jedem Container-Start ein zufälliger
Schlüssel generiert – dann sind nach jedem Neustart alle abgemeldet. Für einen
stabilen Schlüssel:

```bash
cp .env.example .env
python3 -c "import secrets; print(secrets.token_hex(32))"  # in .env eintragen
```

`docker-compose.yml` liest `.env` automatisch.

## Fertiges Image statt selbst bauen

CI veröffentlicht bei jedem Push auf `main` und bei `v*.*.*`-Tags ein
Multi-Arch-Image (`linux/amd64` + `linux/arm64`) nach GHCR. Statt `build: .`
kann `docker-compose.yml` auch direkt darauf zeigen:

```yaml
    image: ghcr.io/steven-streller/basealert:latest
```

## Updates

```bash
docker compose pull   # nur bei image: statt build:
docker compose up -d --build
```
