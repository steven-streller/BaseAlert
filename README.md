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
setzen, um weitere Registrierungen zu blockieren – bestehende Accounts können sich
weiterhin einloggen.

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

Es liegen keine fertigen Manifeste im Repo – BaseAlert ist ein einzelner
Pod mit einer kleinen PVC für die SQLite-Datenbank, das lässt sich leicht in
euer eigenes Manifest-/GitOps-Setup einbauen. Beispiel zum Anpassen (Namespace,
Storage-Class, Image-Tag, Hostname etc. auf eure Umgebung übertragen):

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: basealert
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: basealert-data
  namespace: basealert
spec:
  accessModes:
    - ReadWriteOnce
  # k3s ships "local-path" by default; swap for longhorn, nfs-subdir-*, etc.
  storageClassName: local-path
  resources:
    requests:
      storage: 256Mi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: basealert
  namespace: basealert
spec:
  replicas: 1
  strategy:
    type: Recreate # SQLite on a ReadWriteOnce volume can't be shared by two pods
  selector:
    matchLabels:
      app: basealert
  template:
    metadata:
      labels:
        app: basealert
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        # Makes the PVC group-writable for GID 1000 - the image's own UID/GID
        # 1000 user isn't guaranteed to own whatever the provisioner hands back.
        fsGroup: 1000
      # Only needed if the ghcr.io/<owner>/basealert package is private:
      #   kubectl create secret docker-registry ghcr-creds -n basealert \
      #     --docker-server=ghcr.io --docker-username=<gh-user> --docker-password=<gh-pat>
      # imagePullSecrets:
      #   - name: ghcr-creds
      containers:
        - name: basealert
          image: ghcr.io/steven-streller/basealert:latest
          ports:
            - containerPort: 8000
          env:
            - name: TZ
              value: Europe/Berlin
            - name: BASEALERT_DB_PATH
              value: /app/data/basealert.db
            - name: REGISTRATION_ENABLED
              value: "true"
            # Without this secret the app falls back to a random key generated
            # at container start, which logs everyone out on every restart.
            # kubectl create secret generic basealert-secrets -n basealert \
            #   --from-literal=session-secret-key=$(python3 -c "import secrets; print(secrets.token_hex(32))")
            - name: SESSION_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: basealert-secrets
                  key: session-secret-key
                  optional: true
          volumeMounts:
            - name: data
              mountPath: /app/data
          resources:
            requests:
              cpu: 50m
              memory: 96Mi
            limits:
              cpu: 300m
              memory: 256Mi
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8000
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8000
            initialDelaySeconds: 15
            periodSeconds: 30
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: basealert-data
---
apiVersion: v1
kind: Service
metadata:
  name: basealert
  namespace: basealert
spec:
  selector:
    app: basealert
  ports:
    - port: 8000
      targetPort: 8000
```

Ressourcen-Bedarf ist minimal: im Leerlauf ~70 MB RAM, kurze CPU-Spitzen nur
während eines Scrape-Laufs.

- **Erreichbarkeit**: Der `Service` oben ist `ClusterIP`. Für Zugriff von außen
  entweder `kubectl port-forward -n basealert svc/basealert 8000:8000` nutzen,
  oder eine `Ingress`-Ressource mit echtem Hostnamen ergänzen (k3s bringt dafür
  Traefik mit).
- **Non-root**: Das Image läuft als `appuser` (UID/GID 1000). Der
  `securityContext` oben (`runAsUser`/`runAsGroup`/`fsGroup: 1000`) ist nötig,
  damit die PVC für diese UID beschreibbar gemountet wird – ohne passenden
  `fsGroup` bzw. ohne einen zur UID passenden `/etc/passwd`-Eintrag im Image
  zeigt sich das als "I have no name!" in einer interaktiven Shell.

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
