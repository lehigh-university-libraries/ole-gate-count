import threading
import time
import requests
from xml.etree import ElementTree
import logging
import os
from datetime import datetime
from ..models.database import GateCountModel

logger = logging.getLogger(__name__)


class GateCounterService:
    """Service for managing gate counting in background"""

    def __init__(self):
        self.gate_urls = self._get_gate_urls()
        self.running = False
        self.thread = None

    def _get_gate_urls(self):
        """Get gate URLs from environment variable"""
        urls_env = os.getenv("OLE_GATE_URLS", "")
        if not urls_env:
            logger.warning("OLE_GATE_URLS is empty - gate counting will be disabled")
            return []
        return [url.strip() for url in urls_env.split(",") if url.strip()]

    def start(self):
        """Start the gate counter service"""
        if not self.gate_urls:
            logger.info("No gate URLs configured, gate counting disabled")
            return

        if self.running:
            logger.warning("Gate counter service is already running")
            return

        self.running = True
        self.thread = threading.Thread(target=self._worker, daemon=True)
        self.thread.start()
        logger.info(f"Gate counter service started with {len(self.gate_urls)} gates")

    def stop(self):
        """Stop the gate counter service"""
        self.running = False
        if self.thread:
            self.thread.join(timeout=5)
        logger.info("Gate counter service stopped")

    def _worker(self):
        """Background worker that runs the gate counting logic"""
        while self.running:
            try:
                now = datetime.now()
                s = 3600 - (now.minute * 60 + now.second)
                logger.info(f"Waiting {s} seconds until top of the hour...")

                # Sleep in small increments to allow graceful shutdown
                for _ in range(s):
                    if not self.running:
                        return
                    time.sleep(1)

                self._record_gate_counts()

            except Exception as e:
                logger.error(f"Error in gate counter worker: {e}")
                time.sleep(60)  # Wait a minute before retrying

    def _record_gate_counts(self):
        """Record counts for all configured gates"""
        for i, url in enumerate(self.gate_urls):
            gate_name = self._get_gate_name(url, i)
            self._update_gate_count(url, gate_name)

    def _get_gate_name(self, url, index):
        """Determine gate name based on URL or index"""
        url_lower = url.lower()
        if "south" in url_lower:
            return "FM South gate"
        elif "west" in url_lower:
            return "FM West gate"
        else:
            return f"Gate {index + 1}"

    def _update_gate_count(self, gate_url, gate_name):
        """Update count for a specific gate"""
        timestamp = datetime.now()

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
            last = GateCountModel.get_last_count(gate_name)
            if last:
                alarm_diff = alarm_count - last["alarm_count"]
                incoming_diff = incoming - last["incoming_patrons_count"]
                outgoing_diff = outgoing - last["outgoing_patrons_count"]

            # Insert new count
            GateCountModel.insert_count(
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
                f"{gate_name}: alarm={alarm_count}({alarm_diff:+d}), "
                f"in={incoming}({incoming_diff:+d}), out={outgoing}({outgoing_diff:+d})"
            )

        except Exception as e:
            logger.error(f"Error processing {gate_name}: {e}")

    def record_once(self):
        """Record gate counts once (for testing/manual triggering)"""
        if not self.gate_urls:
            logger.warning("No gate URLs configured")
            return

        logger.info("Recording gate counts manually...")
        self._record_gate_counts()
