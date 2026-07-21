from datetime import datetime

from fastapi.testclient import TestClient
from sqlmodel import Session, select

from app.models import Favorite, ListeningWindow, ScrapeStatus, Station, User, UserSetting
from tests.conftest import register


def test_first_registered_user_becomes_admin(client, test_engine):
    register(client, "alice@example.com")
    with Session(test_engine) as session:
        alice = session.exec(select(User).where(User.email == "alice@example.com")).first()
        assert alice.is_admin is True


def test_second_registered_user_is_not_admin(client, test_engine):
    from app.main import app

    register(client, "alice@example.com")
    bob = TestClient(app)
    register(bob, "bob@example.com")

    with Session(test_engine) as session:
        bob_user = session.exec(select(User).where(User.email == "bob@example.com")).first()
        assert bob_user.is_admin is False


def test_non_admin_gets_404_on_admin_pages(client, test_engine):
    from app.main import app

    register(client, "alice@example.com")  # becomes admin
    bob = TestClient(app)
    register(bob, "bob@example.com")  # not admin

    assert bob.get("/admin/users").status_code == 404
    assert bob.get("/admin/health").status_code == 404
    assert bob.post("/admin/users/1/toggle-admin").status_code == 404


def test_unauthenticated_admin_pages_redirect_to_login(client):
    resp = client.get("/admin/users", follow_redirects=False)
    assert resp.status_code == 303
    assert resp.headers["location"] == "/login"


def test_admin_users_page_lists_all_accounts_with_stats(client, test_engine, seed_dj):
    from app.main import app

    register(client, "alice@example.com")
    bob = TestClient(app)
    register(bob, "bob@example.com")
    bob.post(f"/djs/{seed_dj.id}/toggle")

    page = client.get("/admin/users")
    assert page.status_code == 200
    assert "alice@example.com" in page.text
    assert "bob@example.com" in page.text
    assert "1 Favorit" in page.text


def test_admin_can_promote_another_user(client, test_engine):
    from app.main import app

    register(client, "alice@example.com")
    bob = TestClient(app)
    register(bob, "bob@example.com")

    with Session(test_engine) as session:
        bob_id = session.exec(select(User).where(User.email == "bob@example.com")).first().id

    client.post(f"/admin/users/{bob_id}/toggle-admin")

    with Session(test_engine) as session:
        assert session.get(User, bob_id).is_admin is True


def test_admin_cannot_demote_or_delete_self(client, test_engine):
    register(client, "alice@example.com")
    with Session(test_engine) as session:
        alice_id = session.exec(select(User).where(User.email == "alice@example.com")).first().id

    client.post(f"/admin/users/{alice_id}/toggle-admin")
    client.post(f"/admin/users/{alice_id}/delete")

    with Session(test_engine) as session:
        alice = session.get(User, alice_id)
        assert alice is not None
        assert alice.is_admin is True


def test_admin_deleting_user_cascades_related_rows(client, test_engine, seed_dj):
    from app.main import app

    register(client, "alice@example.com")
    bob = TestClient(app)
    register(bob, "bob@example.com")
    bob.post(f"/djs/{seed_dj.id}/toggle")  # favorite
    bob.post("/windows", data={"weekdays": ["0"], "start_time": "08:00", "end_time": "09:00"})
    bob.post("/settings", data={"_section": "ntfy", "ntfy_enabled": "on", "ntfy_topic": "bob-topic"})

    with Session(test_engine) as session:
        bob_id = session.exec(select(User).where(User.email == "bob@example.com")).first().id
        assert session.exec(select(Favorite).where(Favorite.user_id == bob_id)).first() is not None
        assert (
            session.exec(select(ListeningWindow).where(ListeningWindow.user_id == bob_id)).first() is not None
        )
        assert session.exec(select(UserSetting).where(UserSetting.user_id == bob_id)).first() is not None

    client.post(f"/admin/users/{bob_id}/delete")

    with Session(test_engine) as session:
        assert session.get(User, bob_id) is None
        assert session.exec(select(Favorite).where(Favorite.user_id == bob_id)).first() is None
        assert session.exec(select(ListeningWindow).where(ListeningWindow.user_id == bob_id)).first() is None
        assert session.exec(select(UserSetting).where(UserSetting.user_id == bob_id)).first() is None


def test_admin_health_page_shows_stations(client):
    register(client, "alice@example.com")
    page = client.get("/admin/health")
    assert page.status_code == 200
    assert "TechnoBase.FM" in page.text
    assert "Noch nicht gescrapt" in page.text


def test_admin_health_page_shows_error_status(client, test_engine):
    register(client, "alice@example.com")
    with Session(test_engine) as session:
        station = session.exec(select(Station).where(Station.key == "technobase.fm")).first()
        session.add(
            ScrapeStatus(
                station_id=station.id,
                last_attempt_at=datetime(2026, 7, 21, 12, 0),
                last_error="Connection timed out",
                consecutive_errors=3,
            )
        )
        session.commit()

    page = client.get("/admin/health")
    assert "Connection timed out" in page.text
    assert "3x in Folge" in page.text


def test_admin_can_set_scrape_interval(client):
    register(client, "alice@example.com")
    resp = client.post(
        "/admin/health/scrape-interval", data={"scrape_interval_minutes": "42"}, follow_redirects=False
    )
    assert resp.status_code == 303
    assert resp.headers["location"] == "/admin/health"

    page = client.get("/admin/health")
    assert 'value="42"' in page.text


def test_non_admin_cannot_set_scrape_interval(client):
    from app.main import app

    register(client, "alice@example.com")  # becomes admin
    bob = TestClient(app)
    register(bob, "bob@example.com")

    assert (
        bob.post("/admin/health/scrape-interval", data={"scrape_interval_minutes": "42"}).status_code == 404
    )
