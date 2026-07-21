from sqlmodel import Session, create_engine, select, text

from app.models import User


def test_init_db_backfills_is_admin_column_on_pre_existing_user_table(tmp_path, monkeypatch):
    """Simulates upgrading a BaseAlert install from before the admin flag
    existed: a `user` table without `is_admin` already has rows in it."""
    db_path = tmp_path / "legacy.db"
    engine = create_engine(f"sqlite:///{db_path}", connect_args={"check_same_thread": False})

    with engine.connect() as conn:
        conn.execute(
            text(
                "CREATE TABLE user ("
                "id INTEGER PRIMARY KEY, email VARCHAR NOT NULL UNIQUE, "
                "password_hash VARCHAR NOT NULL, created_at DATETIME NOT NULL)"
            )
        )
        conn.execute(
            text(
                "INSERT INTO user (id, email, password_hash, created_at) VALUES (1, 'old@example.com', 'x', '2026-01-01')"
            )
        )
        conn.commit()

    monkeypatch.setattr("app.db.DB_PATH", str(db_path))
    monkeypatch.setattr("app.db.engine", engine)

    from app.db import init_db

    init_db()

    with Session(engine) as session:
        user = session.exec(select(User).where(User.email == "old@example.com")).first()
        assert user is not None
        assert user.is_admin is False
