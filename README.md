# BaseAlert

![CI](https://github.com/steven-streller/BaseAlert/actions/workflows/ci.yml/badge.svg)
![Docker Publish](https://github.com/steven-streller/BaseAlert/actions/workflows/docker-publish.yml/badge.svg)
![Docs](https://github.com/steven-streller/BaseAlert/actions/workflows/docs.yml/badge.svg)
![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)

Überwacht die Sendepläne von TechnoBase.FM, HouseTime.FM, HardBase.FM und TranceBase.FM
und schickt eine Benachrichtigung, wenn ein favorisierter DJ auflegt – egal auf welchem
der vier Sender. Mehrbenutzerfähig: jeder Account hat seine eigenen Favoriten,
Zeitfenster und Benachrichtigungskanäle.

**Docs: <https://steven-streller.github.io/BaseAlert/>**

Dort steht alles Weitere: Setup-Anleitungen für Entwicklung, Linux (systemd),
Docker, Podman und Kubernetes, die vollständige Feature-Übersicht sowie die
Referenz aller Umgebungsvariablen.

## Schnellstart

```bash
docker compose up -d --build
```

Danach unter <http://localhost:8000> registrieren und loslegen.

## Lizenz

MIT, siehe [LICENSE](LICENSE).
