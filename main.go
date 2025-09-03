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

type MonthlyStats struct {
	Month     string `json:"month"`
	Entrances int    `json:"entrances"`
}

type RecentStats struct {
	TotalEntrances int `json:"total_entrances"`
	TotalExits     int `json:"total_exits"`
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

	mux.HandleFunc("/health", app.handleHealth)

	mux.HandleFunc(scriptName+"/", app.handleIndex)
	mux.HandleFunc(scriptName+"/query", app.handleQuery)
	mux.HandleFunc(scriptName+"/monthly_stats", app.handleMonthlyStats)
	mux.HandleFunc(scriptName+"/recent_stats", app.handleRecentStats)
	mux.HandleFunc(scriptName+"/download_csv", app.handleDownloadCSV)

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
	dbConfig := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=Local",
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
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "unhealthy",
			"service":  "ole-gate-count",
			"database": "disconnected",
			"error":    err.Error(),
		}); err != nil {
			slog.Error("Failed to encode JSON response", "error", err)
		}
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
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "unhealthy",
			"service":  "ole-gate-count",
			"database": "error",
			"error":    err.Error(),
		}); err != nil {
			slog.Error("Failed to encode JSON response", "error", err)
		}
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

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
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

	t, err := template.ParseFiles("templates/index.html")
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
		ScriptName: scriptName,
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
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}); err != nil {
			slog.Error("Failed to encode JSON response", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    results,
		"count":   len(results),
	}); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
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
	if _, err := w.Write([]byte("timestamp,gate_name,alarm_count,alarm_diff,incoming_patrons_count,incoming_diff,outgoing_patrons_count,outgoing_diff\n")); err != nil {
		slog.Error("Failed to write CSV header", "error", err)
		return
	}

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
		if _, err := w.Write([]byte(line)); err != nil {
			slog.Error("Failed to write CSV line", "error", err)
			return
		}
	}
}

func (app *App) handleMonthlyStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the past year of monthly entrance data
	oneYearAgo := time.Now().AddDate(-1, 0, 0)

	query := `
		SELECT 
			CONCAT(YEAR(timestamp), "-", LPAD(MONTH(timestamp), 2, '0')) as month,
			SUM(incoming_diff) as total_entrances
		FROM lib_gate_counts 
		WHERE timestamp >= ? AND incoming_diff > 0
		GROUP BY YEAR(timestamp), MONTH(timestamp)
		ORDER BY YEAR(timestamp), MONTH(timestamp)
	`

	rows, err := app.db.Query(query, oneYearAgo)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}); err != nil {
			slog.Error("Failed to encode JSON response", "error", err)
		}
		return
	}
	defer rows.Close()

	var results []MonthlyStats
	for rows.Next() {
		var stat MonthlyStats
		err := rows.Scan(&stat.Month, &stat.Entrances)
		if err != nil {
			slog.Error("Failed to scan monthly stats", "error", err)
			continue
		}
		results = append(results, stat)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    results,
	}); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}

func (app *App) handleRecentStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get the past 3 hours of data
	threeHoursAgo := time.Now().Add(-3 * time.Hour)

	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN incoming_diff > 0 THEN incoming_diff ELSE 0 END), 0) as total_entrances,
			COALESCE(SUM(CASE WHEN outgoing_diff > 0 THEN outgoing_diff ELSE 0 END), 0) as total_exits
		FROM lib_gate_counts 
		WHERE timestamp >= ?
	`

	var stats RecentStats
	err := app.db.QueryRow(query, threeHoursAgo).Scan(&stats.TotalEntrances, &stats.TotalExits)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}); err != nil {
			slog.Error("Failed to encode JSON response", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    stats,
	}); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
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
		args = append(args, startDate+" 00:00:00")
	}

	if endDate != "" {
		query += " AND timestamp <= ?"
		args = append(args, endDate+" 23:59:59")
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
