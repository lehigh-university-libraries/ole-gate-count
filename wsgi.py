#!/usr/bin/env python3
"""
WSGI entry point for the application
"""
from app import create_app

application = create_app()

if __name__ == "__main__":
    application.run(host="0.0.0.0", port=8080)
