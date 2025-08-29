package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/health") {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		statusWriter := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		next.ServeHTTP(statusWriter, r)
		duration := time.Since(start)
		slog.Info(r.Method,
			"path", r.URL.Path,
			"status", statusWriter.statusCode,
			"duration", duration,
			"client_ip", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
	})
}

type GateCount struct {
	Timestamp            time.Time `json:"timestamp"`
	GateName             string    `json:"gate_name"`
	AlarmCount           int       `json:"alarm_count"`
	AlarmDiff            int       `json:"alarm_diff"`
	IncomingPatronsCount int       `json:"incoming_patrons_count"`
	IncomingDiff         int       `json:"incoming_diff"`
	OutgoingPatronsCount int       `json:"outgoing_patrons_count"`
	OutgoingDiff         int       `json:"outgoing_diff"`
}

type GateXMLResponse struct {
	Count0 int `xml:"count0"`
	Count1 int `xml:"count1"`
	Count2 int `xml:"count2"`
}

type App struct {
	db       *sql.DB
	gateURLs []string
}

var scriptName string

func main() {
	// Setup timezone
	tz := os.Getenv("TZ")
	if tz == "" {
		tz = "America/New_York"
	}
	location, err := time.LoadLocation(tz)
	if err != nil {
		slog.Error("Failed to load timezone", "timezone", tz, "error", err)
		os.Exit(1)
	}
	time.Local = location
	slog.Info("Timezone set", "timezone", tz)

	// Setup structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	scriptName = os.Getenv("SCRIPT_NAME")

	app, err := NewApp()
	if err != nil {
		slog.Error("Failed to create app", "error", err)
		os.Exit(1)
	}
	defer app.db.Close()

	// Start background gate counter
	go app.gateCounterWorker()

	// Setup routes
	mux := http.NewServeMux()

	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/health", app.handleHealth)
	mux.HandleFunc("/query", app.handleQuery)
	mux.HandleFunc("/download_csv", app.handleDownloadCSV)

	// Apply logging middleware
	handler := LoggingMiddleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("Starting server", "port", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func NewApp() (*App, error) {
	// Database connection
	dbConfig := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		getEnv("MARIADB_USER", "ole"),
		getDBPassword(),
		getEnv("MARIADB_HOST", "mariadb"),
		getEnv("MARIADB_PORT", "3306"),
		getEnv("MARIADB_NAME", "ole"),
	)

	db, err := sql.Open("mysql", dbConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Gate URLs
	gateURLsStr := os.Getenv("OLE_GATE_URLS")
	var gateURLs []string
	if gateURLsStr != "" {
		gateURLs = strings.Split(gateURLsStr, ",")
		for i, url := range gateURLs {
			gateURLs[i] = strings.TrimSpace(url)
		}
	}

	return &App{
		db:       db,
		gateURLs: gateURLs,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getDBPassword() string {
	if data, err := os.ReadFile("/var/run/secrets/OLE_DB_PASSWORD"); err == nil {
		return strings.TrimSpace(string(data))
	}
	return "password"
}

func (app *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	if err := app.db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "unhealthy",
			"service":  "ole-gate-count",
			"database": "disconnected",
			"error":    err.Error(),
		})
		return
	}

	// Check for recent entries (last hour)
	var count int
	var latestEntry sql.NullTime

	oneHourAgo := time.Now().Add(-time.Hour)
	err := app.db.QueryRow(`
		SELECT COUNT(*) as recent_count, MAX(timestamp) as latest_entry 
		FROM lib_gate_counts 
		WHERE timestamp >= ?
	`, oneHourAgo).Scan(&count, &latestEntry)

	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "unhealthy",
			"service":  "ole-gate-count",
			"database": "error",
			"error":    err.Error(),
		})
		return
	}

	status := "healthy"
	httpStatus := http.StatusOK
	if count == 0 {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)

	response := map[string]interface{}{
		"status":         status,
		"service":        "ole-gate-count",
		"database":       "connected",
		"recent_entries": count,
	}

	if latestEntry.Valid {
		response["latest_entry"] = latestEntry.Time.Format(time.RFC3339)
	} else {
		response["latest_entry"] = nil
	}

	json.NewEncoder(w).Encode(response)
}

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Get unique gate names
	rows, err := app.db.Query("SELECT DISTINCT gate_name FROM lib_gate_counts ORDER BY gate_name")
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		slog.Error("Failed to get gate names", "error", err)
		return
	}
	defer rows.Close()

	var gateNames []string
	for rows.Next() {
		var gateName string
		if err := rows.Scan(&gateName); err != nil {
			slog.Error("Failed to scan gate name", "error", err)
			continue
		}
		gateNames = append(gateNames, gateName)
	}

	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Gate Count Query Interface</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 1200px; margin: 0 auto; padding: 20px; background-color: #f5f5f5; }
        .container { background: white; padding: 30px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #333; text-align: center; margin-bottom: 30px; }
        .form-group { margin-bottom: 20px; }
        label { display: block; margin-bottom: 5px; font-weight: bold; color: #555; }
        select, input[type="date"] { width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 16px; }
        .date-range { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
        .button-group { display: flex; gap: 10px; margin-top: 20px; }
        button { padding: 12px 24px; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; font-weight: bold; }
        .btn-primary { background-color: #007bff; color: white; }
        .btn-primary:hover { background-color: #0056b3; }
        .btn-secondary { background-color: #28a745; color: white; }
        .btn-secondary:hover { background-color: #1e7e34; }
        .btn-secondary:disabled { background-color: #6c757d; cursor: not-allowed; }
        .loading { text-align: center; margin: 20px 0; color: #666; }
        .error { background-color: #f8d7da; color: #721c24; padding: 12px; border-radius: 4px; margin: 20px 0; }
        .results-info { margin: 20px 0; padding: 10px; background-color: #d4edda; color: #155724; border-radius: 4px; }
        table { width: 100%; border-collapse: collapse; margin-top: 20px; background: white; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid #ddd; }
        th { background-color: #f8f9fa; font-weight: bold; color: #495057; }
        tr:hover { background-color: #f5f5f5; }
        .table-container { max-height: 600px; overflow-y: auto; border: 1px solid #ddd; border-radius: 4px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Gate Count Query Interface</h1>
        
        <form id="queryForm">
            <div class="form-group">
                <label for="gate_name">Gate Name:</label>
                <select id="gate_name" name="gate_name">
                    <option value="all">All Gates</option>
                    {{range .GateNames}}
                    <option value="{{.}}">{{.}}</option>
                    {{end}}
                </select>
            </div>
            
            <div class="date-range">
                <div class="form-group">
                    <label for="start_date">Start Date:</label>
                    <input type="date" id="start_date" name="start_date">
                </div>
                
                <div class="form-group">
                    <label for="end_date">End Date:</label>
                    <input type="date" id="end_date" name="end_date">
                </div>
            </div>
            
            <div class="form-group">
                <label for="order_by">Sort Order:</label>
                <select id="order_by" name="order_by">
                    <option value="asc">Oldest First (ASC)</option>
                    <option value="desc">Newest First (DESC)</option>
                </select>
            </div>
            
            <div class="button-group">
                <button type="submit" class="btn-primary">Query Data</button>
                <button type="button" id="downloadCsv" class="btn-secondary" disabled>Download CSV</button>
            </div>
        </form>
        
        <div id="loading" class="loading" style="display: none;">Loading results...</div>
        <div id="error" class="error" style="display: none;"></div>
        <div id="resultsInfo" class="results-info" style="display: none;"></div>
        
        <div id="results" style="display: none;">
            <div class="table-container">
                <table id="resultsTable">
                    <thead>
                        <tr>
                            <th>Timestamp</th>
                            <th>Gate Name</th>
                            <th>Alarm Count</th>
                            <th>Alarm Diff</th>
                            <th>Incoming Count</th>
                            <th>Incoming Diff</th>
                            <th>Outgoing Count</th>
                            <th>Outgoing Diff</th>
                        </tr>
                    </thead>
                    <tbody></tbody>
                </table>
            </div>
        </div>
    </div>

    <script>
        const scriptName = '{{.ScriptName}}';
        const queryForm = document.getElementById('queryForm');
        const loading = document.getElementById('loading');
        const error = document.getElementById('error');
        const results = document.getElementById('results');
        const resultsInfo = document.getElementById('resultsInfo');
        const resultsTable = document.getElementById('resultsTable').getElementsByTagName('tbody')[0];
        const downloadCsv = document.getElementById('downloadCsv');
        
        let currentQueryData = null;

        queryForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            
            const formData = {
                gate_name: document.getElementById('gate_name').value,
                start_date: document.getElementById('start_date').value,
                end_date: document.getElementById('end_date').value,
                order_by: document.getElementById('order_by').value
            };
            
            currentQueryData = formData;
            
            loading.style.display = 'block';
            error.style.display = 'none';
            results.style.display = 'none';
            resultsInfo.style.display = 'none';
            downloadCsv.disabled = true;
            
            try {
                const response = await fetch('` + scriptName + `/query', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(formData)
                });
                
                const data = await response.json();
                
                if (data.success) {
                    displayResults(data.data, data.count);
                } else {
                    showError(data.error);
                }
            } catch (err) {
                showError('Network error: ' + err.message);
            } finally {
                loading.style.display = 'none';
            }
        });

        downloadCsv.addEventListener('click', async () => {
            if (!currentQueryData) return;
            
            try {
                const response = await fetch('` + scriptName + `/download_csv', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(currentQueryData)
                });
                
                if (response.ok) {
                    const blob = await response.blob();
                    const url = window.URL.createObjectURL(blob);
                    const a = document.createElement('a');
                    a.href = url;
                    const filename = response.headers.get('Content-Disposition')?.split('filename=')[1] || 'gate_counts.csv';
                    a.download = filename;
                    document.body.appendChild(a);
                    a.click();
                    window.URL.revokeObjectURL(url);
                    document.body.removeChild(a);
                } else {
                    showError('Failed to download CSV');
                }
            } catch (err) {
                showError('Download error: ' + err.message);
            }
        });

        function displayResults(data, count) {
            resultsTable.innerHTML = '';
            resultsInfo.textContent = 'Found ' + count + ' records';
            resultsInfo.style.display = 'block';
            
            data.forEach(row => {
                const tr = document.createElement('tr');
                tr.innerHTML = '<td>' + row.timestamp + '</td><td>' + row.gate_name + '</td><td>' + row.alarm_count + '</td><td>' + row.alarm_diff + '</td><td>' + row.incoming_patrons_count + '</td><td>' + row.incoming_diff + '</td><td>' + row.outgoing_patrons_count + '</td><td>' + row.outgoing_diff + '</td>';
                resultsTable.appendChild(tr);
            });
            
            results.style.display = 'block';
            downloadCsv.disabled = false;
        }

        function showError(message) {
            error.textContent = message;
            error.style.display = 'block';
        }
    </script>
</body>
</html>`

	t, err := template.New("index").Parse(tmpl)
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		slog.Error("Failed to parse template", "error", err)
		return
	}

	data := struct {
		GateNames  []string
		ScriptName string
	}{
		GateNames:  gateNames,
		ScriptName: os.Getenv("SCRIPT_NAME"),
	}

	w.Header().Set("Content-Type", "text/html")
	if err := t.Execute(w, data); err != nil {
		slog.Error("Failed to execute template", "error", err)
	}
}

func (app *App) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		GateName  string `json:"gate_name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		OrderBy   string `json:"order_by"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	results, err := app.queryGateCounts(req.GateName, req.StartDate, req.EndDate, req.OrderBy)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    results,
		"count":   len(results),
	})
}

func (app *App) handleDownloadCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		GateName  string `json:"gate_name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		OrderBy   string `json:"order_by"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	results, err := app.queryGateCounts(req.GateName, req.StartDate, req.EndDate, req.OrderBy)
	if err != nil {
		http.Error(w, "Query failed", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("gate_counts_%s.csv", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	// Write CSV header
	w.Write([]byte("timestamp,gate_name,alarm_count,alarm_diff,incoming_patrons_count,incoming_diff,outgoing_patrons_count,outgoing_diff\n"))

	// Write CSV data
	for _, record := range results {
		line := fmt.Sprintf("%s,%s,%d,%d,%d,%d,%d,%d\n",
			record.Timestamp.Format("2006-01-02 15:04:05"),
			record.GateName,
			record.AlarmCount,
			record.AlarmDiff,
			record.IncomingPatronsCount,
			record.IncomingDiff,
			record.OutgoingPatronsCount,
			record.OutgoingDiff,
		)
		w.Write([]byte(line))
	}
}

func (app *App) queryGateCounts(gateName, startDate, endDate, orderBy string) ([]GateCount, error) {
	query := "SELECT timestamp, gate_name, alarm_count, alarm_diff, incoming_patrons_count, incoming_diff, outgoing_patrons_count, outgoing_diff FROM lib_gate_counts WHERE 1=1"
	args := []interface{}{}

	if gateName != "" && gateName != "all" {
		query += " AND gate_name LIKE ?"
		args = append(args, "%"+gateName+"%")
	}

	if startDate != "" {
		query += " AND timestamp >= ?"
		args = append(args, startDate)
	}

	if endDate != "" {
		query += " AND timestamp <= ?"
		args = append(args, endDate)
	}

	// Add order by clause
	if orderBy == "desc" {
		query += " ORDER BY timestamp DESC"
	} else {
		query += " ORDER BY timestamp ASC"
	}

	rows, err := app.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []GateCount
	for rows.Next() {
		var gc GateCount
		err := rows.Scan(&gc.Timestamp, &gc.GateName, &gc.AlarmCount, &gc.AlarmDiff,
			&gc.IncomingPatronsCount, &gc.IncomingDiff, &gc.OutgoingPatronsCount, &gc.OutgoingDiff)
		if err != nil {
			return nil, err
		}
		results = append(results, gc)
	}

	return results, nil
}

func (app *App) gateCounterWorker() {
	if len(app.gateURLs) == 0 {
		slog.Info("No gate URLs configured, gate counting disabled")
		return
	}

	slog.Info("Starting gate counter worker", "gates", len(app.gateURLs))

	for {
		now := time.Now()
		// Calculate seconds until next hour
		nextHour := now.Truncate(time.Hour).Add(time.Hour)
		waitTime := nextHour.Sub(now)

		slog.Info("Waiting until top of the hour", "wait_seconds", int(waitTime.Seconds()))
		time.Sleep(waitTime)

		if err := app.recordGateCounts(); err != nil {
			slog.Error("Failed to record gate counts", "error", err)
		}
	}
}

func (app *App) recordGateCounts() error {
	slog.Info("Recording gate counts")

	for i, url := range app.gateURLs {
		gateName := app.getGateName(url, i)
		if err := app.updateGateCount(url, gateName); err != nil {
			slog.Error("Failed to update gate count", "gate", gateName, "error", err)
		}
	}

	slog.Info("Gate counting completed successfully")
	return nil
}

func (app *App) getGateName(url string, index int) string {
	urlLower := strings.ToLower(url)
	if strings.Contains(urlLower, "south") {
		return "FM South gate"
	} else if strings.Contains(urlLower, "west") {
		return "FM West gate"
	}
	return fmt.Sprintf("Gate %d", index+1)
}

func (app *App) updateGateCount(gateURL, gateName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", gateURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch gate data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad response from %s: %d", gateURL, resp.StatusCode)
	}

	var xmlResp GateXMLResponse
	if err := xml.NewDecoder(resp.Body).Decode(&xmlResp); err != nil {
		return fmt.Errorf("failed to decode XML: %w", err)
	}

	// Get current counts
	alarmCount := xmlResp.Count0
	incoming := xmlResp.Count1
	outgoing := xmlResp.Count2

	// Calculate diffs
	alarmDiff, incomingDiff, outgoingDiff := 0, 0, 0
	last, err := app.getLastCount(gateName)
	if err != nil {
		slog.Warn("Failed to get last count", "gate", gateName, "error", err)
	} else if last != nil {
		alarmDiff = alarmCount - last.AlarmCount
		incomingDiff = incoming - last.IncomingPatronsCount
		outgoingDiff = outgoing - last.OutgoingPatronsCount
	}

	// Insert new count
	timestamp := time.Now()
	if err := app.insertCount(timestamp, gateName, alarmCount, alarmDiff, incoming, incomingDiff, outgoing, outgoingDiff); err != nil {
		return fmt.Errorf("failed to insert count: %w", err)
	}

	slog.Info("Gate count updated",
		"gate", gateName,
		"alarm", fmt.Sprintf("%d(%+d)", alarmCount, alarmDiff),
		"incoming", fmt.Sprintf("%d(%+d)", incoming, incomingDiff),
		"outgoing", fmt.Sprintf("%d(%+d)", outgoing, outgoingDiff),
	)

	return nil
}

func (app *App) getLastCount(gateName string) (*GateCount, error) {
	var gc GateCount
	err := app.db.QueryRow(`
		SELECT timestamp, gate_name, alarm_count, alarm_diff, incoming_patrons_count, incoming_diff, outgoing_patrons_count, outgoing_diff 
		FROM lib_gate_counts 
		WHERE gate_name = ? 
		ORDER BY timestamp DESC 
		LIMIT 1
	`, gateName).Scan(&gc.Timestamp, &gc.GateName, &gc.AlarmCount, &gc.AlarmDiff,
		&gc.IncomingPatronsCount, &gc.IncomingDiff, &gc.OutgoingPatronsCount, &gc.OutgoingDiff)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &gc, nil
}

func (app *App) insertCount(timestamp time.Time, gateName string, alarmCount, alarmDiff, incoming, incomingDiff, outgoing, outgoingDiff int) error {
	_, err := app.db.Exec(`
		INSERT INTO lib_gate_counts (timestamp, gate_name, alarm_count, alarm_diff, incoming_patrons_count, incoming_diff, outgoing_patrons_count, outgoing_diff) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, timestamp, gateName, alarmCount, alarmDiff, incoming, incomingDiff, outgoing, outgoingDiff)
	return err
}
