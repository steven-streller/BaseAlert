# BaseAlert

BaseAlert beobachtet die Sender von [WeAreOne.FM](https://weareone.fm/) (TechnoBase, HouseTime, HardBase,
TranceBase) und schickt eine [Pushover](https://pushover.net/)-Nachricht, wenn

- einer deiner **Lieblings-DJs** auflegt, oder
- in einem von dir festgelegten **Zeitslot** irgendein DJ live ist.

Es ist ein einzelnes statisches Go-Binary in einem `FROM scratch`-Image von rund 8 MB, mit strukturierten
Logs für Loki und einem `/metrics`-Endpunkt für Prometheus.

## Schnellstart

Du brauchst ein Pushover-Konto, eine dort angelegte Anwendung (<https://pushover.net/apps/build>) für den
API-Token und deinen User-Key.

```sh
cp .env.example .env                               # PUSHOVER_TOKEN und PUSHOVER_USER eintragen
cp config/config.example.yaml config/config.yaml   # Favoriten und Zeitslots eintragen
podman compose up -d --build                       # oder: docker compose up -d --build

podman exec basealert /basealert test              # Test-Push aufs Handy
podman exec basealert /basealert now               # wer sendet gerade, welche Regel greift
podman exec basealert /basealert djs               # DJ-Namen und IDs aus dem Sendeplan
```

Läuft podman rootless, muss `config/config.yaml` für andere lesbar sein (`chmod 644`), weil der Prozess im
Container als unprivilegierter Benutzer 65534 läuft. Zugangsdaten stehen nicht in dieser Datei.

Nach einem Update baust und startest du neu mit `podman compose up -d --build --force-recreate`. Ohne
`--force-recreate` lässt podman-compose den laufenden Container mit dem alten Image weiterlaufen. Was schon
gemeldet wurde, bleibt im Volume erhalten, ein Neustart wiederholt keine Push.

Dasselbe gilt für `.env`: Umgebungsvariablen liest ein Container nur beim Erzeugen. Nach einer Änderung an
`.env` brauchst du `podman compose up -d --force-recreate`. Änderungen an `config.yaml` wirken dagegen von
selbst.

Läuft etwas nicht, zeigt `podman logs basealert` den Grund. Kann BaseAlert gar nicht erst starten, etwa weil
`PUSHOVER_TOKEN` fehlt, beendet es sich mit einem Fehler, aber frühestens 30 Sekunden nach dem Start. Die
Pause verhindert, dass eine Neustart-Regel den Container mehrmals pro Sekunde neu startet.

## Wann kommt eine Push?

| Begriff | Bedeutung |
|---|---|
| DJ-Session | Derselbe DJ ist ununterbrochen auf einem Sender live. |
| Live-Block | Ein Sender ist ununterbrochen live, egal mit wie vielen DJs nacheinander. |
| Slot-Phase | Für einen Sender ist mindestens ein Zeitslot offen. Überlappende und angrenzende Slots verschmelzen. |

1. **Favorit**: Beginnt die Session eines Favoriten, kommt eine Push. Immer, auch außerhalb der Zeitslots.
2. **Zeitslot**: Ist ein Sender während einer Slot-Phase live, kommt eine Push je Live-Block. Das gilt, wenn
   ein DJ im Slot beginnt, und auch, wenn zum Slot-Beginn schon jemand sendet. Übernimmt nahtlos ein anderer
   DJ, kommt keine weitere Push.
3. **Nie doppelt**: Eine Session wird höchstens einmal gemeldet. Wurde sie schon als Favorit gemeldet, gilt
   der Live-Block für den Zeitslot als gemeldet.
4. **Aussetzer**: Fällt ein Sender für weniger als 10 Minuten auf die Playlist zurück, gilt das als
   Verbindungsabbruch des DJs. Session und Live-Block laufen weiter.

Beispiel mit einem Slot freitags 18–24 Uhr; F und G sind Favoriten:

| Zeit | Ereignis | Push |
|---|---|---|
| 17:00 | Favorit F geht live | Favorit |
| 18:00 | Slot beginnt, F läuft noch | keine, Session schon gemeldet |
| 19:00 | DJ B übernimmt nahtlos | keine, Live-Block gilt als gemeldet |
| 21:00 | Playlist | keine |
| 22:00 | DJ C geht live | Zeitslot |
| 23:00 | Favorit G übernimmt nahtlos | Favorit |

So sehen die Nachrichten aus. Der Link öffnet den Player mit dem passenden Sender, und die Push verschwindet
von selbst, wenn die Sendung vorbei ist.

| | Favorit | Zeitslot |
|---|---|---|
| Titel | BlueCore ist live | TechnoBase.FM ist live |
| Text | TechnoBase.FM – Happy Hardcore Bass Kick<br>Happy Hardcore · 20:00–22:00 Uhr | BlueCore – Happy Hardcore Bass Kick<br>Happy Hardcore · bis 22:00 Uhr |

Dazu kommen Meldungen über BaseAlert selbst: `config.yaml fehlerhaft`, `Sender-API nicht erreichbar` und
`Sender-API wieder erreichbar`.

## Konfiguration

### config.yaml

Die Datei wird bei jedem Abruf auf Änderungen geprüft, ein Neustart ist nicht nötig. Ist sie fehlerhaft, bleibt
die letzte gültige Fassung aktiv und du bekommst eine Push mit der Fehlerstelle. Unbekannte Schlüssel gelten
als Fehler, damit Tippfehler auffallen. Alle Angaben sind optional.

```yaml
timezone: Europe/Berlin          # für Zeitslots und die Uhrzeiten in der Push
poll_interval: 60s               # Abstand der Abrufe, mindestens 30s
stations: [TechnoBase, HardBase] # weglassen = alle vier Sender

favorites:
  - BlueCore                     # Name, Groß-/Kleinschreibung egal
  - 373169                       # oder DJ-ID, bleibt bei Umbenennung gültig

slots:
  - days: [Fr, Sa]               # Mo Di Mi Do Fr Sa So; weglassen = täglich
    from: "20:00"
    to: "02:00"                  # früher als from: der Slot läuft über Mitternacht
    stations: [TechnoBase]       # weglassen = alle beobachteten Sender

notify:
  favorite: { priority: 0, sound: "" }   # priority -2 (lautlos) bis 1 (durchbricht Ruhezeiten)
  slot:     { priority: 0, sound: "" }

outage_alert_after: 15m          # Push bei API-Ausfall nach dieser Zeit; 0 = aus
```

Bei einem Slot über Mitternacht zählt der Wochentag für den Beginn: `days: [Fr]` mit `20:00`–`02:00` läuft
von Freitag 20 Uhr bis Samstag 2 Uhr. `from` gleich `to` bedeutet 24 Stunden.

Mounte das Verzeichnis `config/`, nicht die einzelne Datei. Viele Editoren ersetzen die Datei beim Speichern,
und ein Datei-Mount zeigt dann weiter auf die alte Fassung.

### Umgebungsvariablen

| Variable | Standard | Zweck |
|---|---|---|
| `PUSHOVER_TOKEN` | – | API-Token der Pushover-Anwendung, Pflicht |
| `PUSHOVER_USER` | – | User- oder Group-Key, Pflicht |
| `PUSHOVER_DEVICE` | alle Geräte | nur an dieses Gerät senden |
| `BASEALERT_CONFIG` | `/config/config.yaml` | Pfad der Konfiguration |
| `BASEALERT_STATE` | `/data/state.json` | merkt sich, was gemeldet wurde; leer = nicht speichern |
| `BASEALERT_HTTP_ADDR` | `:8080` | Adresse für `/metrics` und `/healthz`; leer = kein HTTP-Server |
| `BASEALERT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `BASEALERT_LOG_FORMAT` | `json` | `text` zum Lesen im Terminal |

Ohne State-Datei meldet BaseAlert nach einem Neustart laufende Sendungen erneut.

### Kommandos

| Kommando | Zweck |
|---|---|
| `basealert` | Daemon |
| `basealert now` | Live-Status der beobachteten Sender, mit DJ-ID und ob Favorit oder Zeitslot greift |
| `basealert djs` | DJs aus dem Sendeplan von gestern bis in sieben Tagen, mit ID und nächster Sendung |
| `basealert test` | Test-Push, prüft die Zugangsdaten |
| `basealert health` | fragt `/healthz` des laufenden Daemons ab, für Healthchecks ohne Shell und curl |
| `basealert version` | Version |

## Observability

Metriken zeigen, dass etwas passiert ist. Logs zeigen, warum.

| Frage | Antwort |
|---|---|
| Läuft BaseAlert und sieht es die Sender-API? | `up`, `basealert_poll_last_success_timestamp_seconds`, `basealert_polls_total` |
| Was ist gerade auf den Sendern los? | `basealert_station_live`, `basealert_station_favorite_live`, `basealert_station_slot_active` |
| Wurde ich benachrichtigt, hat es geklappt? | `basealert_notifications_total` |
| Warum kam eine Push oder keine? | Logs `station.live` und `slot.started` mit `decision` und `reason` |
| Ist meine Config aktiv und gültig? | `basealert_config_last_reload_successful`, Logs `config.loaded` und `config.invalid` |
| Wer hat wann aufgelegt? | Logs `station.live` |

### Metriken

`GET /metrics` liefert das Prometheus-Textformat. Mit vier Sendern sind es rund 200 Serien, etwa 130 davon
sind Go- und Prozessmetriken.

| Metrik | Typ | Labels | Bedeutung |
|---|---|---|---|
| `basealert_station_live` | Gauge | `station` | 1 = DJ live, 0 = Playlist |
| `basealert_station_favorite_live` | Gauge | `station` | 1 = ein Favorit ist live |
| `basealert_station_slot_active` | Gauge | `station` | 1 = ein Zeitslot ist offen |
| `basealert_notifications_total` | Counter | `kind`, `result`, `station` | Push-Versuche |
| `basealert_polls_total` | Counter | `result` | Abrufe der Sender-API |
| `basealert_poll_duration_seconds` | Histogramm | – | Dauer eines Abrufs, Buckets 0,05 bis 10 s |
| `basealert_poll_last_success_timestamp_seconds` | Gauge | – | Zeitpunkt des letzten erfolgreichen Abrufs |
| `basealert_config_last_reload_successful` | Gauge | – | 1 = aktuelle config.yaml ist gültig |
| `basealert_config_last_reload_success_timestamp_seconds` | Gauge | – | Zeitpunkt des letzten gültigen Ladens |
| `basealert_config_favorites` | Gauge | – | Favoriten in der aktiven Config |
| `basealert_config_slots` | Gauge | – | Zeitslots in der aktiven Config |
| `basealert_build_info` | Gauge | `version`, `goversion` | konstant 1 |
| `go_*`, `process_*` | – | – | Laufzeit und Prozess |

Labelwerte:

- `kind`: `favorite`, `slot`, dazu `outage`, `recovery` und `config_error` für Meldungen über BaseAlert selbst
  (diese tragen kein `station`-Label).
- `result` bei Pushes: `sent` (angenommen), `failed` (vorübergehender Fehler, wird wiederholt), `rejected`
  (endgültig abgelehnt, wird nicht wiederholt).
- `result` bei Abrufen: `ok`, `timeout`, `network`, `http_status`, `invalid_response`.

Was du wissen solltest:

- **Die Sender-Gauges enden bei einem Ausfall.** Sie werden nur ausgeliefert, solange der letzte erfolgreiche
  Abruf höchstens drei Intervalle alt ist. Bei einem API-Ausfall entsteht eine Lücke statt eines eingefrorenen
  „live“. Sie zeigen die Rohsicht der API, ohne die 10 Minuten Aussetzer-Toleranz.
- **Counter beginnen bei 0**, für jede bekannte Kombination von Labels. `increase()` stimmt damit ab dem
  ersten Ereignis.
- **Labels kommen nur aus festen Mengen.** DJ- und Shownamen stehen deshalb in den Logs, nicht in Metriken.
- **Beschreibungstexte** (`# HELP`) haben die `basealert_*`-Metriken. Die `go_*`- und `process_*`-Metriken der
  Bibliothek haben keine; `promtool check metrics` weist darauf hin.

Ausdrücke, die sich als Alarme eignen:

```promql
# BaseAlert sieht die Sender-API seit zehn Minuten nicht mehr
time() - basealert_poll_last_success_timestamp_seconds > 600

# Die aktuelle config.yaml ist fehlerhaft
basealert_config_last_reload_successful == 0

# Pushover lehnt Nachrichten ab (Zugangsdaten, Kontingent)
increase(basealert_notifications_total{result="rejected"}[1h]) > 0
```

Dazu der übliche Alarm auf `up == 0` für den Fall, dass der Prozess nicht läuft.

### Logs

Eine JSON-Zeile je Ereignis auf stdout. Die Felder sind flach und in snake_case, Loki liefert sie mit `| json`
ohne Umbenennung.

- `time`: UTC, RFC 3339
- `level`: `debug`, `info`, `warn`, `error`
- `msg`: kurzer englischer Klartext
- `event`: stabiler Name des Ereignisses, darauf filterst du

Auf `info` stehen nur Zustandswechsel und Entscheidungen, das sind unter 100 Zeilen am Tag. Jeden einzelnen
Abruf zeigt erst `debug`. Auch Ausgaben von Bibliotheken und ein Panic erscheinen als JSON-Zeile. Token und
User-Key werden nie geloggt.

| `event` | Pegel | Wichtige Felder |
|---|---|---|
| `app.started` | info | `version`, `config_path`, `state_path`, `http_addr` |
| `app.stopping` | info, error | `reason` |
| `app.exit_delayed` | info | `delay_seconds` |
| `app.panic` | error | `error`, `stack` |
| `http.listening` | info | `http_addr` |
| `http.error` | warn, error | `error` |
| `config.loaded` | info | `config_hash`, `stations`, `favorites`, `slots`, `poll_interval`, `timezone`, `reload` |
| `config.invalid` | error | `error`, `config_hash`, `active_config_kept` |
| `state.restored` | info | `stations`, `discarded` |
| `state.load_failed`, `state.save_failed` | warn | `error` |
| `poll.completed` | debug | `duration_ms`, `live_stations` |
| `poll.failed` | warn | `error`, `error_kind`, `http_status`, `consecutive_failures`, `duration_ms` |
| `upstream.outage` | error | `since`, `consecutive_failures`, `error` |
| `upstream.recovered` | info | `since`, `outage_seconds`, `failed_polls` |
| `station.live` | info | `station`, `dj`, `dj_id`, `show`, `style`, `show_start`, `show_end`, `favorite`, `slot_active`, `decision`, `reason` |
| `station.playlist` | info | `station`, `dj`, `dj_id`, `live_seconds` |
| `slot.started` | info | `station`, `live`, `dj`, `dj_id`, `favorite`, `decision`, `reason` |
| `slot.ended` | info | `station`, `live` |
| `notification.sent` | info | `kind`, `station`, `dj`, `dj_id`, `show`, `title`, `pushover_request`, `duration_ms` |
| `notification.failed` | warn | `kind`, `station`, `error`, `http_status`, `duration_ms` |
| `notification.rejected` | error | `kind`, `station`, `error`, `http_status`, `duration_ms` |
| `library.log` | warn | Meldung einer Bibliothek in `msg` |

`station.live` erscheint bei jedem Sendungsbeginn, auch bei einem nahtlosen DJ-Wechsel. `station.playlist`
erscheint, wenn ein Live-Block endet, also 10 Minuten nach dem letzten DJ.

`decision` ist `notify_favorite`, `notify_slot` oder `none`. Bei `none` nennt `reason` den Grund:

| `reason` | Bedeutung |
|---|---|
| `no_rule_matched` | kein Favorit und kein offener Zeitslot |
| `session_already_notified` | diese Session wurde schon gemeldet |
| `block_already_notified` | der Live-Block wurde in dieser Slot-Phase schon gemeldet |
| `not_live` | der Zeitslot begann, aber es läuft die Playlist |

Abfragen für Loki. Den Selektor am Anfang musst du an die Labels deines Log-Agents anpassen.

```logql
# Warum kam auf TechnoBase (k)eine Push?
{container="basealert"} | json | event=~"station.live|slot.started" | station="TechnoBase"

# Alle gesendeten Pushes
{container="basealert"} | json | event="notification.sent" | line_format "{{.title}}"

# Wer hat wann aufgelegt
{container="basealert"} | json | event="station.live" | line_format "{{.station}}: {{.dj}} – {{.show}}"

# Alles, was nicht nach Plan lief
{container="basealert"} | json | level=~"warn|error"
```

### Dashboard

[grafana/basealert-dashboard.json](grafana/basealert-dashboard.json) zeigt den Live-Status je Sender als
Zeitleiste, die Zeitslots, gesendete Pushes, fehlgeschlagene Abrufe, den Betrieb und drei Log-Ansichten.
Gesendete Pushes erscheinen zusätzlich als Markierung in allen Diagrammen.

Oben wählst du die Prometheus- und die Loki-Datenquelle, den Job und den Log-Selektor. Der Log-Selektor steht
auf `{container="basealert"}` und hängt davon ab, welche Labels dein Log-Agent vergibt.

Die Farben folgen der Bedeutung: Blau steht für live, Orange für Favoriten, Türkis für Zeitslots, Grau für
Playlist. Eine Lücke in einer Zeitleiste bedeutet, dass keine Daten vorlagen.

### Anbindung an kube-prometheus-stack und Loki

Die Schnipsel sind Beispiele, Namen und Labels musst du anpassen.

**Scrape, wenn BaseAlert im Cluster läuft.** Ein Service mit benanntem Port und ein ServiceMonitor:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: basealert
  labels:
    release: kube-prometheus-stack   # siehe unten
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: basealert
  endpoints:
    - port: metrics                  # Name des Service-Ports, der auf 8080 zeigt
      interval: 30s
```

**Scrape, wenn BaseAlert außerhalb läuft**, zum Beispiel per Compose auf einem anderen Host:

```yaml
apiVersion: monitoring.coreos.com/v1alpha1
kind: ScrapeConfig
metadata:
  name: basealert
  labels:
    release: kube-prometheus-stack
spec:
  staticConfigs:
    - targets: ["mein-host.example:8080"]
      labels:
        job: basealert
```

Die mitgelieferte `compose.yaml` veröffentlicht den Port nur auf `127.0.0.1`. Für einen Scrape von außen musst
du die Bindung ändern.

**Das Label `release`.** Der kube-prometheus-stack wählt in der Voreinstellung nur ServiceMonitore und
ScrapeConfigs aus, die das Label `release` mit dem Namen des Helm-Release tragen. Was dein Prometheus
tatsächlich auswählt, zeigt:

```sh
kubectl get prometheus -A -o jsonpath='{range .items[*]}{.spec.serviceMonitorSelector}{"\n"}{.spec.scrapeConfigSelector}{"\n"}{end}'
```

**Dashboard.** Der Grafana-Sidecar des Stacks lädt Dashboards aus ConfigMaps mit dem Label
`grafana_dashboard: "1"`:

```sh
kubectl create configmap basealert-dashboard --from-file=grafana/basealert-dashboard.json
kubectl label configmap basealert-dashboard grafana_dashboard=1
```

Alternativ importierst du die Datei in Grafana von Hand.

**Logs.** Im Cluster sammelt dein Log-Agent stdout von selbst ein. Läuft BaseAlert außerhalb, braucht der Host
einen Agent, der die Container-Logs nach Loki schickt.

**Betrieb in Kubernetes.**

- Genau eine Replica mit `strategy: Recreate`. Zwei Pods gleichzeitig würden doppelt pushen.
- `config.yaml` als ConfigMap nach `/config` mounten (als Verzeichnis). Änderungen werden übernommen, sobald
  das Kubelet sie ausgerollt hat.
- `PUSHOVER_TOKEN` und `PUSHOVER_USER` aus einem Secret.
- `/data` auf ein kleines PersistentVolume legen. Mit `emptyDir` kann nach einem Pod-Neustart eine laufende
  Sendung einmal doppelt gemeldet werden.
- Liveness-Probe auf `GET /healthz`. Der Endpunkt meldet 503, wenn die Abruf-Schleife länger als drei
  Intervalle nicht durchgelaufen ist. Er hängt bewusst nicht an der Sender-API, damit deren Ausfall keinen
  Neustart auslöst.
- Das Image läuft als Benutzer 65534 ohne Root-Rechte und kommt mit `readOnlyRootFilesystem: true` und ohne
  Capabilities aus.

## Image

`FROM scratch` hat weder Zertifikate noch Zeitzonendaten noch eine Shell. Das Image enthält deshalb genau
drei Dinge: das Binary, die CA-Zertifikate und ein leeres Verzeichnis `/data` für die State-Datei. Die
Zeitzonendaten stecken im Binary, der Healthcheck ist ein Unterkommando.

Gemessen für linux/amd64 mit Go 1.27:

| Baustein | Größe |
|---|---|
| Go-Basis: HTTPS-Client mit TLS | 5,48 MB |
| JSON | +0,77 MB |
| HTTP-Server für `/metrics` und `/healthz` | +0,20 MB |
| Strukturierte Logs | +0,11 MB |
| YAML-Konfiguration | +0,49 MB |
| Metrik-Bibliothek mit Go- und Prozessmetriken | +0,18 MB |
| BaseAlert selbst und der Rest der Standardbibliothek | +0,31 MB |
| Zeitzonendaten | +0,41 MB |
| **Binary** | **7,97 MB** |
| CA-Zertifikate | +0,18 MB |
| **Image** | **8,16 MB** |

Der HTTP/2-Client ist weggelassen (Build-Tag `nethttpomithttp2`), das spart 0,59 MB. Beide APIs sprechen
HTTP/1.1. Im Betrieb belegt der Prozess rund 15 MB Arbeitsspeicher.

```sh
podman build -t basealert --build-arg VERSION=1.0.0 .
podman build -t basealert --platform linux/arm64 .   # Cross-Build, die Build-Stage läuft nativ
```

## CI und fertige Images

Der Workflow [.github/workflows/ci.yml](.github/workflows/ci.yml) läuft bei jedem Push und jedem Pull
Request:

1. **Tests**: `gofmt`, `go vet`, die Tests mit Race-Detector und noch einmal so, wie das Image gebaut wird
   (ohne cgo, mit den Build-Tags). Go-Version und Build-Tags liest der Workflow aus dem Dockerfile, sie stehen
   also nur an einer Stelle.
2. **Image**: baut für amd64, startet das Image einmal und prüft seine Größe gegen ein Budget von 10 MB. Danach
   baut er für amd64 und arm64.

Veröffentlicht wird nach `ghcr.io/<besitzer>/basealert`, und nur bei Pushes:

| Auslöser | Tags des Images | `basealert version` |
|---|---|---|
| Push auf `main` | `edge` | `edge-<Commit>` |
| Tag `v1.2.3` | `1.2.3`, `1.2`, `latest` | `1.2.3` |
| Tag `v1.3.0-rc.1` | `1.3.0-rc.1` | `1.3.0-rc.1` |
| Pull Request | keine, es wird nur gebaut | – |

Eine Version veröffentlichst du mit einem Tag:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Um das fertige Image statt eines lokalen Builds zu nutzen, ersetzt du in `compose.yaml` den Abschnitt `build`
und die Zeile `image` durch `image: ghcr.io/<besitzer>/basealert:1.0.0`. Ob das Paket öffentlich oder privat
ist, stellst du auf GitHub in den Einstellungen des Pakets ein. Ein privates Paket braucht zum Ziehen eine
Anmeldung, im Cluster ein `imagePullSecret`.

Die Actions sind auf Commits festgelegt, nicht auf verschiebbare Tags. [Dependabot](.github/dependabot.yml)
hält diese Festlegungen, die Go-Module und das Basis-Image im Dockerfile aktuell.

## Entwicklung

```sh
go test ./...
BASEALERT_CONFIG=config/config.yaml go run . now

# Daemon lokal, mit PUSHOVER_TOKEN und PUSHOVER_USER in der Umgebung:
BASEALERT_CONFIG=config/config.yaml BASEALERT_STATE= BASEALERT_LOG_FORMAT=text go run .
```

| Paket | Aufgabe |
|---|---|
| `internal/wao` | Client der Sender-API, tolerantes JSON, Plausibilitätsprüfung |
| `internal/config` | `config.yaml` laden und prüfen, Zeitslot-Logik |
| `internal/engine` | Entscheidungslogik ohne I/O, dazu die State-Datei |
| `internal/pushover` | Pushover-Client mit Fehlerklassifikation |
| `internal/obs` | Log-Format, Ereignisnamen, Metriken, HTTP-Endpunkte |
| `main` | Abruf-Schleife, Push-Texte, Kommandos |

Metriknamen und Ereignisnamen sind eine Schnittstelle, an der Dashboards und Abfragen hängen. Tests halten sie
fest: Der Metrik-Katalog steht in `internal/obs/testdata/metric_families.golden`, und ein Test prüft, dass das
Dashboard und dieses README nur Metriken und Ereignisse nennen, die es gibt.

## Grenzen

- Die Sender-API (`api.tb-group.fm`) ist die des Web-Players, undokumentiert und ohne Zusage. Ändert sich ihr
  Format, erkennt BaseAlert das als fehlgeschlagenen Abruf und meldet den Ausfall, statt still nichts zu tun.
- BaseAlert fragt die API so oft ab wie der Web-Player selbst, einmal pro Minute.
- Ein neuer Sender im Netzwerk muss im Code ergänzt werden (`internal/wao`).
- Übersteht eine Sendung ihr geplantes Ende und fällt genau dann ein Neustart, kann eine Push doppelt kommen.
