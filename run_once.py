#!/usr/bin/env python3
"""
Script to run gate counter once for testing
"""
from app.services.gate_counter import GateCounterService
import logging

logging.basicConfig(level=logging.INFO)

if __name__ == "__main__":
    service = GateCounterService()
    service.record_once()
