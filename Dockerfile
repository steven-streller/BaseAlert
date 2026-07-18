FROM python:3.14-slim

ENV TZ=Europe/Berlin \
    PYTHONUNBUFFERED=1 \
    BASEALERT_DB_PATH=/app/data/basealert.db

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends tzdata && rm -rf /var/lib/apt/lists/*

# Matches the UID/GID many clusters enforce for non-root pods (e.g. via
# PodSecurityStandards or a runAsUser policy). Without a matching /etc/passwd
# entry, that UID shows up as "I have no name!" in a shell.
RUN groupadd --gid 1000 appuser && useradd --uid 1000 --gid appuser --create-home --shell /usr/sbin/nologin appuser

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app

RUN mkdir -p /app/data && chown -R appuser:appuser /app

USER appuser

VOLUME ["/app/data"]
EXPOSE 8000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
