FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .

RUN pip install --no-cache-dir -r requirements.txt

COPY gate_counter.py .

RUN useradd --create-home --shell /bin/bash app && chown -R app:app /app
USER app

ENV \
  MARIADB_HOST=mariadb \
  MARIADB_USER=ole \
  MARIADB_NAME=ole \
  MARIADB_PORT=3306 \
  OLE_GATE_URLS= \
  SCRAPE_INTERVAL=300

CMD ["python", "gate_counter.py"]
