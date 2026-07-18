# Mitwirken

## Ablauf

`main` ist geschützt: jede Änderung läuft über einen Branch + Pull Request,
direkte Pushes werden abgelehnt (auch für Repo-Admins). Zum Mergen müssen
zwei Status-Checks grün sein:

- `lint-and-test` – `ruff check .` + `pytest`
- `docker-build` – das Image muss bauen

Es ist kein Review-Approval erforderlich, damit auch Solo-Änderungen ohne
zweite Person gemergt werden können – die CI-Checks sind das eigentliche Gate.

```bash
git checkout -b feature/mein-feature
# Änderungen ...
git push -u origin feature/mein-feature
gh pr create
```

## Lokal einrichten

Siehe [Entwicklung](setup/development.md) für venv, Tests, Lint und
Projektstruktur.

```bash
pytest              # muss durchlaufen
ruff check .         # muss sauber sein
```

Ruff-Regeln, die absichtlich ignoriert werden (siehe `pyproject.toml`):
`UP007`/`UP045` (Stil-Präferenz `Optional[X]` statt `X | None` für
SQLModel-Felder) und `B008` (der übliche FastAPI-`Depends(...)`-Default, den
Ruffs Bugbear-Regel fälschlich als Problem meldet).

## Tests

Neue Routen/Logik sollten nach Möglichkeit mit abgedeckt werden –
`tests/conftest.py` stellt dafür zwei Fixtures bereit:

- `test_engine`: frische SQLite-Datei pro Test, in alle Module verdrahtet, die
  `engine` importieren
- `client`: ein `TestClient` über die echte App, **ohne** das Startup-Event
  auszulösen (kein echter Scheduler, kein Netzwerkzugriff im Test)

```python
def test_my_new_route(client):
    register(client, "alice@example.com")
    resp = client.get("/my-route")
    assert resp.status_code == 200
```

## Abhängigkeiten

Dependabot hält `requirements*.txt` und GitHub Actions aktuell und öffnet
wöchentlich PRs. Minor/Patch-Updates mergen automatisch, sobald die Checks
grün sind (`.github/workflows/dependabot-automerge.yml`); Major-Updates
bleiben zur manuellen Prüfung offen.

## Neuen Benachrichtigungskanal hinzufügen

Siehe [Architektur](architecture.md#benachrichtigungskanale-appnotificationspy) –
ein Eintrag im `CHANNELS`-Dict in `app/notifications.py` reicht, die
Einstellungen-Seite rendert die Formularfelder automatisch daraus.
