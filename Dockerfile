FROM python:3.14-alpine AS builder

WORKDIR /app

# bcrypt/uvloop/httptools ship musllinux wheels for most releases, but not
# always for a brand-new CPython version — keep a build toolchain around
# only in this stage so it never ends up in the final image.
RUN apk add --no-cache --virtual .build-deps gcc musl-dev libffi-dev cargo

COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

FROM python:3.14-alpine

ENV TZ=Europe/Berlin \
    PYTHONUNBUFFERED=1 \
    BASEALERT_DB_PATH=/app/data/basealert.db

WORKDIR /app

RUN apk add --no-cache tzdata

# Matches the UID/GID many clusters enforce for non-root pods (e.g. via
# PodSecurityStandards or a runAsUser policy). Without a matching /etc/passwd
# entry, that UID shows up as "I have no name!" in a shell.
RUN addgroup -g 1000 appuser && adduser -D -u 1000 -G appuser -s /sbin/nologin appuser

COPY --from=builder /install /usr/local

COPY app ./app

RUN mkdir -p /app/data && chown -R appuser:appuser /app

USER appuser

VOLUME ["/app/data"]
EXPOSE 8000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
