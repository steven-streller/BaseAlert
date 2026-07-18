FROM python:3.12-slim

ENV TZ=Europe/Berlin \
    PYTHONUNBUFFERED=1 \
    BASEALERT_DB_PATH=/app/data/basealert.db

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends tzdata && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app ./app

VOLUME ["/app/data"]
EXPOSE 8000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
