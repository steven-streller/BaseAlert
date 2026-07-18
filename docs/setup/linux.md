# Linux (systemd)

Für den Betrieb direkt auf einem Server, ohne Container – z.B. auf einem
Raspberry Pi oder einem kleinen VPS.

## Installation

```bash
sudo useradd --system --create-home --home-dir /opt/basealert --shell /usr/sbin/nologin basealert
sudo -u basealert git clone https://github.com/steven-streller/BaseAlert.git /opt/basealert/app
cd /opt/basealert/app
sudo -u basealert python3 -m venv /opt/basealert/venv
sudo -u basealert /opt/basealert/venv/bin/pip install -r requirements.txt
sudo mkdir -p /opt/basealert/data
sudo chown basealert:basealert /opt/basealert/data
```

## Session-Secret

```bash
python3 -c "import secrets; print(secrets.token_hex(32))"
```

Das Ergebnis brauchst du gleich für die systemd-Unit.

## systemd-Unit

`/etc/systemd/system/basealert.service`:

```ini
[Unit]
Description=BaseAlert
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=basealert
Group=basealert
WorkingDirectory=/opt/basealert/app
Environment=TZ=Europe/Berlin
Environment=BASEALERT_DB_PATH=/opt/basealert/data/basealert.db
Environment=SESSION_SECRET_KEY=<hier den generierten Schlüssel eintragen>
Environment=REGISTRATION_ENABLED=true
ExecStart=/opt/basealert/venv/bin/uvicorn app.main:app --host 127.0.0.1 --port 8000
Restart=on-failure
RestartSec=5

# Defense in depth - the app doesn't need broad filesystem/network access
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/basealert/data
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now basealert
sudo systemctl status basealert
journalctl -u basealert -f
```

Der Dienst horcht hier bewusst nur auf `127.0.0.1` – für Zugriff von außen
einen Reverse Proxy davorsetzen (siehe unten), statt uvicorn direkt an
`0.0.0.0` zu binden.

## Reverse Proxy (Caddy)

Am einfachsten für TLS via Let's-Encrypt-Automatik. `/etc/caddy/Caddyfile`:

```caddyfile
basealert.example.com {
    reverse_proxy 127.0.0.1:8000
}
```

```bash
sudo systemctl reload caddy
```

Alternativ nginx mit einem klassischen `proxy_pass http://127.0.0.1:8000;`
Server-Block plus certbot für TLS.

## Updates

```bash
cd /opt/basealert/app
sudo -u basealert git pull
sudo -u basealert /opt/basealert/venv/bin/pip install -r requirements.txt
sudo systemctl restart basealert
```
