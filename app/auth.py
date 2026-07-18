from fastapi import HTTPException, Request
from sqlmodel import Session

from app.db import engine
from app.models import User

SESSION_USER_KEY = "user_id"


def login_user(request: Request, user: User) -> None:
    request.session[SESSION_USER_KEY] = user.id


def logout_user(request: Request) -> None:
    request.session.pop(SESSION_USER_KEY, None)


def get_current_user(request: Request) -> User | None:
    user_id = request.session.get(SESSION_USER_KEY)
    if not user_id:
        return None
    with Session(engine) as session:
        return session.get(User, user_id)


def require_user(request: Request) -> User:
    user = get_current_user(request)
    if not user:
        raise HTTPException(status_code=303, headers={"Location": "/login", "HX-Redirect": "/login"})
    return user
