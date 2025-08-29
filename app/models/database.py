import pymysql
import os
from datetime import datetime

DB_CONFIG = {
    "host": os.getenv("MARIADB_HOST", "mariadb"),
    "user": os.getenv("MARIADB_USER", "ole"),
    "password": (
        open("/var/run/secrets/OLE_DB_PASSWORD").read().strip()
        if os.path.exists("/var/run/secrets/OLE_DB_PASSWORD")
        else "password"
    ),
    "database": os.getenv("MARIADB_NAME", "ole"),
    "port": int(os.getenv("MARIADB_PORT", "3306")),
}


def get_db_connection():
    """Get database connection with proper charset and unicode support"""
    return pymysql.connect(charset="utf8mb4", use_unicode=True, **DB_CONFIG)


class GateCountModel:
    """Model for gate count operations"""

    @staticmethod
    def get_unique_gate_names():
        """Get list of unique gate names from database"""
        with get_db_connection() as conn:
            cursor = conn.cursor()
            cursor.execute(
                "SELECT DISTINCT gate_name FROM lib_gate_counts ORDER BY gate_name"
            )
            return [row[0] for row in cursor.fetchall()]

    @staticmethod
    def get_last_count(gate_name):
        """Get the most recent count for a specific gate"""
        with get_db_connection() as conn:
            cursor = conn.cursor(pymysql.cursors.DictCursor)
            cursor.execute(
                "SELECT * FROM lib_gate_counts WHERE gate_name = %s ORDER BY timestamp DESC LIMIT 1",
                (gate_name,),
            )
            return cursor.fetchone()

    @staticmethod
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
        """Insert a new gate count record"""
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

    @staticmethod
    def query_counts(gate_name=None, start_date=None, end_date=None):
        """Query gate counts with optional filters"""
        sql = "SELECT * FROM lib_gate_counts WHERE 1=1"
        params = []

        if gate_name and gate_name != "all":
            sql += " AND gate_name LIKE %s"
            params.append(f"%{gate_name}%")

        if start_date:
            sql += " AND timestamp >= %s"
            params.append(start_date)

        if end_date:
            sql += " AND timestamp <= %s"
            params.append(end_date)

        sql += " ORDER BY timestamp DESC"

        with get_db_connection() as conn:
            cursor = conn.cursor(pymysql.cursors.DictCursor)
            cursor.execute(sql, params)
            results = cursor.fetchall()

            # Convert datetime objects to strings for JSON serialization
            for row in results:
                if "timestamp" in row and isinstance(row["timestamp"], datetime):
                    row["timestamp"] = row["timestamp"].strftime("%Y-%m-%d %H:%M:%S")

            return results
