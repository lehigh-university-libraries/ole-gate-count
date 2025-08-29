FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .

RUN pip install --no-cache-dir -r requirements.txt

COPY wsgi.py .
COPY docker-entrypoint.sh .
COPY app/ ./app/

RUN useradd --create-home --shell /bin/bash app && chown -R app:app /app
USER app

ENV \
  TZ=America/New_York \
  MARIADB_HOST=mariadb \
  MARIADB_USER=ole \
  MARIADB_NAME=ole \
  MARIADB_PORT=3306 \
  OLE_GATE_URLS= \
  ADDRESS=0.0.0.0 \
  PORT=8080 \
  WORKERS=1 \
  SCRIPT_NAME=/gate-counts

EXPOSE 8080

CMD ["/app/docker-entrypoint.sh"]
