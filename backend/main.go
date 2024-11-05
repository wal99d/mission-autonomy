package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type Waypoint struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
}

type Geofence struct {
	ID       int       `json:"id"`
	MissionID int      `json:"mission_id"`
	Name     string    `json:"name"`
	Vertices [][2]float64 `json:"vertices"`
}

type Mission struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
}

type Server struct{
	db *sql.DB
	upgrader websocket.Upgrader
}

func NewServer(db *sql.DB) *Server{
	return &Server{
		db: db,
		upgrader: websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
},

	}
}
func initDB(db *sql.DB) (*sql.DB, error){
	var err error
	if err = godotenv.Load(); err!=nil{
		return nil, err
	}
	connStr := fmt.Sprintf("user=%s dbname=%s sslmode=disable password=%s",
		os.Getenv("DB_USER"), os.Getenv("DB_NAME"), os.Getenv("DB_PASSWORD"))
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade:", err)
		return
	}
	defer conn.Close()
	req := r.URL.Query().Get("mission_id")
	if req == ""{
		log.Println("mission_id is empty")
		return
	}else {
		
	missionID, err := strconv.Atoi(req)
	if err !=nil{
		log.Fatal(err)
		// return
	}
	for {
		rows, err := s.db.Query("SELECT latitude, longitude, timestamp FROM waypoints WHERE mission_id = $1 ORDER BY timestamp ASC", missionID)
		if err != nil {
			log.Println("DB Query Error:", err)
			return
		}

		var waypoint Waypoint
		for rows.Next() {
			if err := rows.Scan(&waypoint.Latitude, &waypoint.Longitude, &waypoint.Timestamp); err != nil {
				log.Println("Row Scan Error:", err)
				return
			}
			jsonData, _ := json.Marshal(waypoint)
			err = conn.WriteMessage(websocket.TextMessage, jsonData)
			if err != nil {
				log.Println("WebSocket Write Error:", err)
				return
			}
			time.Sleep(2 * time.Second) // Simulate a delay for each waypoint
		}
		rows.Close()
	}
		}
}

// Calculate total distance and duration of a mission
func (s *Server) calculateMissionSummary(missionID string) (float64, time.Duration) {
	rows, err := s.db.Query("SELECT latitude, longitude, timestamp FROM waypoints WHERE mission_id = $1 ORDER BY timestamp ASC", missionID)
	if err != nil {
		log.Println("DB Query Error:", err)
		return 0, 0
	}
	defer rows.Close()

	var (
		totalDistance float64
		startTime     time.Time
		endTime       time.Time
		prevLat       float64
		prevLon       float64
		firstPoint    = true
	)

	for rows.Next() {
		var latitude, longitude float64
		var timestamp time.Time

		if err := rows.Scan(&latitude, &longitude, &timestamp); err != nil {
			log.Println("Row Scan Error:", err)
			return 0, 0
		}

		if firstPoint {
			startTime = timestamp
			firstPoint = false
		} else {
			totalDistance += haversine(prevLat, prevLon, latitude, longitude)
		}
		prevLat = latitude
		prevLon = longitude
		endTime = timestamp
	}

	duration := endTime.Sub(startTime)
	return totalDistance, duration
}

// Haversine formula to calculate distance between two points
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Radius of Earth in kilometers
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*(math.Pi / 180.0))*math.Cos(lat2*(math.Pi / 180.0))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	missionID := r.URL.Query().Get("mission_id")
	if missionID == "" {
		missionID = "1" // Default mission ID
	}

	distance, duration := s.calculateMissionSummary(missionID)

	data := struct {
		Distance float64
		Duration time.Duration
		Missions []Mission
	}{
		Distance: distance,
		Duration: duration,
		Missions: s.getAllMissions(),
	}

	tmpl, err := template.ParseFiles("templates/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, data)
}

// Get all missions from the database
func (s *Server) getAllMissions() []Mission {
	rows, err := s.db.Query("SELECT id, name, COALESCE(status, 'unknown') FROM missions ORDER BY id ASC")
	if err != nil {
		log.Println("DB Query Error:", err)
		return nil
	}
	defer rows.Close()

	var missions []Mission
	for rows.Next() {
		var mission Mission
		if err := rows.Scan(&mission.ID, &mission.Name, &mission.Status); err != nil {
			log.Println("Row Scan Error:", err)
			continue
		}
		missions = append(missions, mission)
	}

	return missions
}

func main() {
	db, _ := initDB(&sql.DB{})
	srv := NewServer(db)
	http.HandleFunc("/telemetry", srv.handleTelemetry)
	
	http.HandleFunc("/", srv.handleIndex)
	http.HandleFunc("/add_geofence", srv.handleAddGeofence)
	http.HandleFunc("/add_mission", srv.handleAddMission)
	http.HandleFunc("/missions", srv.handleGetMissions)
	http.HandleFunc("/add_waypoint", srv.handleAddWaypoint)
	http.HandleFunc("/clear_all", srv.handleClearAll)
	fmt.Println("Server started at :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Handler to add new geofence
type AddGeofenceRequest struct {
	MissionID int       `json:"mission_id"`
	Name     string    `json:"name"`
	Vertices [][2]float64 `json:"vertices"`
}

func (s *Server) handleAddGeofence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req AddGeofenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if len(req.Vertices) == 0 {
		http.Error(w, "Geofence vertices are empty", http.StatusBadRequest)
		return
	}

	_, err := s.db.Exec("INSERT INTO geofences (mission_id, name) VALUES ($1, $2) RETURNING id", req.MissionID, req.Name)
	if err != nil {
		http.Error(w, "Failed to add geofence", http.StatusInternalServerError)
		return
	}

	for _, vertex := range req.Vertices {
		_, err := s.db.Exec("INSERT INTO geofence_vertices (geofence_id, latitude, longitude) VALUES ((SELECT id FROM geofences WHERE mission_id=$1 AND name=$2), $3, $4)", req.MissionID, req.Name, vertex[0], vertex[1])
		if err != nil {
			http.Error(w, "Failed to add geofence vertex", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// Handler to add a new mission
type AddMissionRequest struct {
	Name string `json:"name"`
}

func (s * Server) handleAddMission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req AddMissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	_, err := s.db.Exec("INSERT INTO missions (name, status) VALUES ($1, 'active')", req.Name)
	if err != nil {
		http.Error(w, "Failed to add mission", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// Handler to get all missions
func (s *Server) handleGetMissions(w http.ResponseWriter, r *http.Request) {
	missions := s.getAllMissions()
	if missions == nil {
		http.Error(w, "No missions found", http.StatusNotFound)
		return
	}

	jsonData, err := json.Marshal(missions)
	if err != nil {
		http.Error(w, "Failed to retrieve missions", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonData)
}

// Handler to add a new waypoint
type AddWaypointRequest struct {
	MissionID int     `json:"mission_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
}

func (s *Server) handleAddWaypoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var req AddWaypointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if req.MissionID == 0 {
		http.Error(w, "Mission ID is required", http.StatusBadRequest)
		return
	}

	_, err := s.db.Exec("INSERT INTO waypoints (mission_id, latitude, longitude, timestamp) VALUES ($1, $2, $3, $4)", req.MissionID, req.Latitude, req.Longitude, req.Timestamp)
	if err != nil {
		http.Error(w, "Failed to add waypoint", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// Handler to clear all missions, waypoints, and geofences
func (s *Server) handleClearAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	_, err := s.db.Exec("DELETE FROM waypoints")
	if err != nil {
		http.Error(w, "Failed to clear waypoints", http.StatusInternalServerError)
		return
	}

	_, err = s.db.Exec("DELETE FROM geofence_vertices")
	if err != nil {
		http.Error(w, "Failed to clear geofence vertices", http.StatusInternalServerError)
		return
	}

	_, err = s.db.Exec("DELETE FROM geofences")
	if err != nil {
		http.Error(w, "Failed to clear geofences", http.StatusInternalServerError)
		return
	}

	_, err = s.db.Exec("DELETE FROM missions")
	if err != nil {
		http.Error(w, "Failed to clear missions", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
