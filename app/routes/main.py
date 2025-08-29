from flask import Blueprint, render_template, request, jsonify, make_response
import csv
import io
from datetime import datetime
from ..models.database import GateCountModel

bp = Blueprint("main", __name__)


@bp.route("/")
def index():
    """Main page with query form"""
    gate_names = GateCountModel.get_unique_gate_names()
    return render_template("index.html", gate_names=gate_names)


@bp.route("/query", methods=["POST"])
def query_data():
    """API endpoint for querying gate count data"""
    try:
        data = request.get_json()
        gate_name = data.get("gate_name")
        start_date = data.get("start_date")
        end_date = data.get("end_date")
        order_by = data.get("order_by", "asc")

        results = GateCountModel.query_counts(gate_name, start_date, end_date, order_by)

        return jsonify({"success": True, "data": results, "count": len(results)})

    except Exception as e:
        return jsonify({"success": False, "error": str(e)}), 500


@bp.route("/download_csv", methods=["POST"])
def download_csv():
    """Download query results as CSV"""
    try:
        data = request.get_json()
        gate_name = data.get("gate_name")
        start_date = data.get("start_date")
        end_date = data.get("end_date")
        order_by = data.get("order_by", "asc")

        results = GateCountModel.query_counts(gate_name, start_date, end_date, order_by)

        # Create CSV in memory
        output = io.StringIO()
        if results:
            fieldnames = results[0].keys()
            writer = csv.DictWriter(output, fieldnames=fieldnames)
            writer.writeheader()

            for row in results:
                # Convert datetime objects to strings if needed
                if "timestamp" in row and isinstance(row["timestamp"], datetime):
                    row["timestamp"] = row["timestamp"].strftime("%Y-%m-%d %H:%M:%S")
                writer.writerow(row)

        # Create response
        response = make_response(output.getvalue())
        response.headers["Content-Type"] = "text/csv"
        response.headers["Content-Disposition"] = (
            f'attachment; filename=gate_counts_{datetime.now().strftime("%Y%m%d_%H%M%S")}.csv'
        )

        return response

    except Exception as e:
        return jsonify({"success": False, "error": str(e)}), 500
