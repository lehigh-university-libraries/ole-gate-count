import requests
from xml.etree import ElementTree
import datetime
import os
import time
import pymysql
import logging
import sys

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

if os.getenv("GATE_URLS") == "":
    logger.info("GATE_URLS is a required env vars")
    sys.exit(1)

DB_CONFIG = {
    "host": os.getenv("MARIADB_HOST", "mariadb"),
    "user": os.getenv("MARIADB_USER", "ole"),
    "password": open("/var/run/secrets/OLE_DB_PASSWORD").read().strip(),
    "database": os.getenv("MARIADB_NAME", "ole"),
    "port": int(os.getenv("MARIADB_PORT", "3306")),
}

GATE_URLS = os.getenv("OLE_GATE_URLS").split(",")
SCRAPE_INTERVAL = int(os.getenv("SCRAPE_INTERVAL", "300"))


def get_db_connection():
    return pymysql.connect(**DB_CONFIG)


def get_last_count(gate_name):
    with get_db_connection() as conn:
        cursor = conn.cursor(pymysql.cursors.DictCursor)
        cursor.execute(
            "SELECT * FROM lib_gate_counts WHERE gate_name=%s ORDER BY timestamp DESC LIMIT 1",
            (gate_name,),
        )
        return cursor.fetchone()


def insert_count(
    timestamp,
    gate_name,
    alarm_count,
    alarm_diff,
    incoming,
    incoming_diff,
    outgoing,
    outgoing_diff,
):
    with get_db_connection() as conn:
        cursor = conn.cursor()
        cursor.execute(
            "INSERT INTO lib_gate_counts VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
            (
                timestamp,
                gate_name,
                alarm_count,
                alarm_diff,
                incoming,
                incoming_diff,
                outgoing,
                outgoing_diff,
            ),
        )
        conn.commit()


def update_gate_count(gate_url, gate_name):
    timestamp = datetime.datetime.now()

    try:
        resp = requests.get(gate_url, timeout=30)
        if resp.status_code != 200:
            logger.error(f"Bad response from {gate_url}: {resp.status_code}")
            return

        root = ElementTree.fromstring(resp.text)

        # Get counts
        alarm_count = int(root.find("count0").text)
        incoming = int(root.find("count1").text)
        outgoing = int(root.find("count2").text)

        # Calculate diffs
        alarm_diff = incoming_diff = outgoing_diff = 0
        last = get_last_count(gate_name)
        if last:
            alarm_diff = alarm_count - last["alarm_count"]
            incoming_diff = incoming - last["incoming_patrons_count"]
            outgoing_diff = outgoing - last["outgoing_patrons_count"]

        # Insert record
        insert_count(
            timestamp,
            gate_name,
            alarm_count,
            alarm_diff,
            incoming,
            incoming_diff,
            outgoing,
            outgoing_diff,
        )

        logger.info(
            f"{gate_name}: alarm={alarm_count}({alarm_diff:+d}), in={incoming}({incoming_diff:+d}), out={outgoing}({outgoing_diff:+d})"
        )

    except Exception as e:
        logger.error(f"Error processing {gate_name}: {e}")


def main():
    logger.info(f"Starting with {len(GATE_URLS)} gates, interval {SCRAPE_INTERVAL}s")

    while True:
        try:
            for i, url in enumerate(GATE_URLS):
                gate_name = (
                    "FM South gate"
                    if "south" in url.lower()
                    else "FM West gate" if "west" in url.lower() else f"Gate {i+1}"
                )
                update_gate_count(url.strip(), gate_name)

            time.sleep(SCRAPE_INTERVAL)

        except KeyboardInterrupt:
            logger.info("Exiting...")
            break


if __name__ == "__main__":
    main()
