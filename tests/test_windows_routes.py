from fastapi.testclient import TestClient

from tests.conftest import register


def test_create_and_list_window(client):
    register(client, "alice@example.com")
    resp = client.post(
        "/windows",
        data={"label": "Feierabend", "weekdays": ["0", "1", "2", "3", "4"], "start_time": "15:00", "end_time": "17:00"},
        follow_redirects=False,
    )
    assert resp.headers["location"] == "/windows?saved=1"

    page = client.get("/windows")
    assert "Feierabend" in page.text
    assert "15:00" in page.text and "17:00" in page.text


def test_create_rejects_start_after_end(client):
    register(client, "alice@example.com")
    client.post(
        "/windows",
        data={"weekdays": ["0"], "start_time": "17:00", "end_time": "15:00"},
    )
    page = client.get("/windows")
    assert "window-row" not in page.text


def test_create_requires_at_least_one_weekday(client):
    register(client, "alice@example.com")
    client.post("/windows", data={"start_time": "15:00", "end_time": "17:00"})
    page = client.get("/windows")
    assert "window-row" not in page.text


def _first_window_id(test_engine):
    from sqlmodel import Session, select

    from app.models import ListeningWindow

    with Session(test_engine) as session:
        return session.exec(select(ListeningWindow)).first().id


def test_delete_own_window(client, test_engine):
    register(client, "alice@example.com")
    client.post("/windows", data={"weekdays": ["0"], "start_time": "15:00", "end_time": "17:00"})

    window_id = _first_window_id(test_engine)
    client.post(f"/windows/{window_id}/delete")

    page = client.get("/windows")
    assert "window-row" not in page.text


def test_cannot_delete_other_users_window(client, test_engine):
    from app.main import app

    alice = client
    register(alice, "alice@example.com")
    alice.post("/windows", data={"weekdays": ["0"], "start_time": "15:00", "end_time": "17:00"})

    bob = TestClient(app)
    register(bob, "bob@example.com")

    window_id = _first_window_id(test_engine)
    bob.post(f"/windows/{window_id}/delete")

    alice_page = alice.get("/windows")
    assert "window-row" in alice_page.text
