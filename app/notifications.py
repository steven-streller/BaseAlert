import logging
import smtplib
from email.mime.text import MIMEText

import requests
from sqlmodel import Session

from app.db import get_user_setting

logger = logging.getLogger("basealert.notifications")


def send_pushover(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    if not cfg["pushover_user_key"] or not cfg["pushover_api_token"]:
        logger.warning("Pushover not configured, skipping")
        return False
    payload = {
        "token": cfg["pushover_api_token"],
        "user": cfg["pushover_user_key"],
        "title": title,
        "message": message,
    }
    if url:
        payload["url"] = url
    try:
        resp = requests.post("https://api.pushover.net/1/messages.json", data=payload, timeout=10)
        resp.raise_for_status()
        return True
    except requests.RequestException as exc:
        logger.error("Pushover send failed: %s", exc)
        return False


def send_ntfy(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    server = cfg["ntfy_server_url"].rstrip("/")
    topic = cfg["ntfy_topic"]
    if not server or not topic:
        logger.warning("ntfy not configured, skipping")
        return False
    headers = {"Title": title.encode("utf-8")}
    if url:
        headers["Click"] = url.encode("utf-8")
    if cfg.get("ntfy_token"):
        headers["Authorization"] = f"Bearer {cfg['ntfy_token']}"
    try:
        resp = requests.post(f"{server}/{topic}", data=message.encode("utf-8"), headers=headers, timeout=10)
        resp.raise_for_status()
        return True
    except requests.RequestException as exc:
        logger.error("ntfy send failed: %s", exc)
        return False


def send_telegram(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    token = cfg["telegram_bot_token"]
    chat_id = cfg["telegram_chat_id"]
    if not token or not chat_id:
        logger.warning("Telegram not configured, skipping")
        return False
    text = f"{title}\n{message}" + (f"\n{url}" if url else "")
    try:
        resp = requests.post(
            f"https://api.telegram.org/bot{token}/sendMessage",
            json={"chat_id": chat_id, "text": text},
            timeout=10,
        )
        resp.raise_for_status()
        return True
    except requests.RequestException as exc:
        logger.error("Telegram send failed: %s", exc)
        return False


def send_discord(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    webhook_url = cfg["discord_webhook_url"]
    if not webhook_url:
        logger.warning("Discord not configured, skipping")
        return False
    content = f"**{title}**\n{message}" + (f"\n{url}" if url else "")
    try:
        resp = requests.post(webhook_url, json={"content": content}, timeout=10)
        resp.raise_for_status()
        return True
    except requests.RequestException as exc:
        logger.error("Discord send failed: %s", exc)
        return False


def send_webhook(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    webhook_url = cfg["webhook_url"]
    if not webhook_url:
        logger.warning("Webhook not configured, skipping")
        return False
    try:
        resp = requests.post(webhook_url, json={"title": title, "message": message, "url": url}, timeout=10)
        resp.raise_for_status()
        return True
    except requests.RequestException as exc:
        logger.error("Webhook send failed: %s", exc)
        return False


def send_email(cfg: dict, title: str, message: str, url: str | None = None) -> bool:
    host = cfg["email_smtp_host"]
    to_addr = cfg["email_to"]
    if not host or not to_addr:
        logger.warning("Email not configured, skipping")
        return False
    body = message + (f"\n\n{url}" if url else "")
    msg = MIMEText(body, "plain", "utf-8")
    msg["Subject"] = title
    msg["From"] = cfg["email_from"] or cfg["email_smtp_user"] or to_addr
    msg["To"] = to_addr
    try:
        port = int(cfg["email_smtp_port"] or 587)
        if port == 465:
            server = smtplib.SMTP_SSL(host, port, timeout=10)
        else:
            server = smtplib.SMTP(host, port, timeout=10)
            if cfg.get("email_use_tls", "true") == "true":
                server.starttls()
        try:
            if cfg["email_smtp_user"]:
                server.login(cfg["email_smtp_user"], cfg["email_smtp_password"])
            server.sendmail(msg["From"], [to_addr], msg.as_string())
        finally:
            server.quit()
        return True
    except (smtplib.SMTPException, OSError) as exc:
        logger.error("Email send failed: %s", exc)
        return False


# Each channel declares its settings fields as (key, label, input_type, placeholder).
# The settings GUI renders these generically, so a new channel only needs an entry here.
CHANNELS = {
    "pushover": {
        "label": "Pushover",
        "send": send_pushover,
        "fields": [
            ("pushover_user_key", "User Key", "text", None),
            ("pushover_api_token", "API Token", "password", None),
        ],
    },
    "ntfy": {
        "label": "ntfy",
        "send": send_ntfy,
        "fields": [
            ("ntfy_server_url", "Server-URL", "text", "https://ntfy.sh"),
            ("ntfy_topic", "Topic", "text", "basealert-steven"),
            ("ntfy_token", "Zugriffstoken (nur bei geschütztem Topic)", "password", None),
        ],
    },
    "telegram": {
        "label": "Telegram",
        "send": send_telegram,
        "fields": [
            ("telegram_bot_token", "Bot Token", "password", None),
            ("telegram_chat_id", "Chat ID", "text", None),
        ],
    },
    "discord": {
        "label": "Discord",
        "send": send_discord,
        "fields": [("discord_webhook_url", "Webhook-URL", "text", None)],
    },
    "webhook": {
        "label": "Webhook",
        "send": send_webhook,
        "fields": [("webhook_url", "URL (POST JSON: title, message, url)", "text", None)],
    },
    "email": {
        "label": "E-Mail",
        "send": send_email,
        "fields": [
            ("email_smtp_host", "SMTP-Host", "text", None),
            ("email_smtp_port", "SMTP-Port", "number", None),
            ("email_use_tls", "STARTTLS verwenden", "checkbox", None),
            ("email_smtp_user", "SMTP-Benutzer", "text", None),
            ("email_smtp_password", "SMTP-Passwort", "password", None),
            ("email_from", "Absender-Adresse", "text", None),
            ("email_to", "Empfänger-Adresse", "text", None),
        ],
    },
}
for _channel in CHANNELS.values():
    _channel["keys"] = [field[0] for field in _channel["fields"]]


def _channel_config(session: Session, user_id: int, channel: str) -> dict:
    return {key: get_user_setting(session, user_id, key) for key in CHANNELS[channel]["keys"]}


def enabled_channels(session: Session, user_id: int) -> list[str]:
    return [c for c in CHANNELS if get_user_setting(session, user_id, f"{c}_enabled") == "true"]


def send_to_channel(
    session: Session, user_id: int, channel: str, title: str, message: str, url: str | None = None
) -> bool:
    cfg = _channel_config(session, user_id, channel)
    return CHANNELS[channel]["send"](cfg, title, message, url)


def notify_all(
    session: Session, user_id: int, title: str, message: str, url: str | None = None
) -> dict[str, bool]:
    results = {}
    for channel in enabled_channels(session, user_id):
        results[channel] = send_to_channel(session, user_id, channel, title, message, url)
    return results
