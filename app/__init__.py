from flask import Flask
import os
import logging
from .services.gate_counter import GateCounterService


def create_app():
    """Application factory pattern"""
    app = Flask(__name__)

    # Configure app
    app.config["APPLICATION_ROOT"] = os.getenv("SCRIPT_NAME", "")

    # Setup logging
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    # Register blueprints
    from .routes import main

    app.register_blueprint(main.bp)

    # Start background services
    if not app.debug:  # Only start in production
        gate_service = GateCounterService()
        gate_service.start()
        app.gate_service = gate_service  # Keep reference to prevent GC

    return app
