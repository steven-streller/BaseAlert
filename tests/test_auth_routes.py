from tests.conftest import register


def test_unauthenticated_dashboard_redirects_to_login(client):
    resp = client.get("/", follow_redirects=False)
    assert resp.status_code == 303
    assert resp.headers["location"] == "/login"


def test_register_creates_account_and_logs_in(client):
    resp = register(client, "alice@example.com")
    assert resp.status_code == 303
    assert resp.headers["location"] == "/"

    dashboard = client.get("/")
    assert dashboard.status_code == 200


def test_register_rejects_invalid_email(client):
    resp = client.post(
        "/register",
        data={"email": "not-an-email", "password": "testpassword1", "password_confirm": "testpassword1"},
        follow_redirects=False,
    )
    assert resp.headers["location"] == "/register?error=email"


def test_register_rejects_short_password(client):
    resp = client.post(
        "/register",
        data={"email": "bob@example.com", "password": "short", "password_confirm": "short"},
        follow_redirects=False,
    )
    assert resp.headers["location"] == "/register?error=password_length"


def test_register_rejects_password_mismatch(client):
    resp = client.post(
        "/register",
        data={"email": "carl@example.com", "password": "testpassword1", "password_confirm": "different1"},
        follow_redirects=False,
    )
    assert resp.headers["location"] == "/register?error=password_mismatch"


def test_register_rejects_duplicate_email(client):
    register(client, "dupe@example.com")
    resp = register(client, "dupe@example.com")
    assert resp.headers["location"] == "/register?error=taken"


def test_registration_disabled_blocks_new_signups(client, monkeypatch):
    monkeypatch.setattr("app.main.REGISTRATION_ENABLED", False)
    resp = register(client, "toolate@example.com")
    assert resp.headers["location"] == "/register"

    page = client.get("/register")
    assert "deaktiviert" in page.text


def test_login_wrong_password_fails(client):
    register(client, "dave@example.com", password="correctpass1")
    client.post("/logout")

    resp = client.post(
        "/login", data={"email": "dave@example.com", "password": "wrongpass1"}, follow_redirects=False
    )
    assert resp.headers["location"] == "/login?error=1"


def test_login_success_allows_dashboard_access(client):
    register(client, "erin@example.com", password="correctpass1")
    client.post("/logout")

    resp = client.post(
        "/login", data={"email": "erin@example.com", "password": "correctpass1"}, follow_redirects=False
    )
    assert resp.headers["location"] == "/"
    assert client.get("/").status_code == 200


def test_logout_clears_session(client):
    register(client, "frank@example.com")
    client.post("/logout")
    resp = client.get("/", follow_redirects=False)
    assert resp.status_code == 303
    assert resp.headers["location"] == "/login"
