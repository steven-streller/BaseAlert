from fastapi.testclient import TestClient

from tests.conftest import register


def test_favorite_toggle_is_isolated_between_users(client, seed_dj):
    from app.main import app

    alice = client
    register(alice, "alice@example.com")

    bob = TestClient(app)
    register(bob, "bob@example.com")

    resp = alice.post(f"/djs/{seed_dj.id}/toggle")
    assert "⭐ Favorit" in resp.text

    # Bob never touched this DJ - must not see it as favorited
    bob_djs = bob.get("/djs")
    assert f'id="dj-row-{seed_dj.id}"' in bob_djs.text
    assert "☆ Favorisieren" in bob_djs.text
    assert "⭐ Favorit" not in bob_djs.text

    # Alice does see it as favorited
    alice_djs = alice.get("/djs")
    assert "⭐ Favorit" in alice_djs.text


def test_favorite_toggle_twice_unfavorites(client, seed_dj):
    register(client, "alice@example.com")
    client.post(f"/djs/{seed_dj.id}/toggle")
    resp = client.post(f"/djs/{seed_dj.id}/toggle")
    assert "☆ Favorisieren" in resp.text


def test_djs_search_filters_by_name(client, seed_dj):
    register(client, "alice@example.com")
    resp = client.get("/djs", params={"q": "testo"})
    assert seed_dj.name in resp.text

    resp = client.get("/djs", params={"q": "no-such-dj"})
    assert seed_dj.name not in resp.text


def test_djs_favorites_only_filter(client, seed_dj, test_engine):
    from sqlmodel import Session

    from app.models import Dj

    with Session(test_engine) as session:
        other_dj = Dj(name="DJ Other")
        session.add(other_dj)
        session.commit()
        session.refresh(other_dj)

    register(client, "alice@example.com")
    client.post(f"/djs/{seed_dj.id}/toggle")

    resp = client.get("/djs", params={"favorites_only": "true"})
    assert seed_dj.name in resp.text
    assert other_dj.name not in resp.text

    resp = client.get("/djs")
    assert seed_dj.name in resp.text
    assert other_dj.name in resp.text
