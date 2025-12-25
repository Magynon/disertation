package main

import (
	"encoding/base64"
	"encoding/json"
	"etl/internal/model"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	dbDSN      = "postgres://user:password@postgres:5432/postgres?sslmode=disable"
	serverPort = ":8080"
)

type Server struct {
	db *gorm.DB
}

type DenormalizedDataResponse struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Diagnosis   string `json:"diagnosis"`
	Concern     bool   `json:"concern"`
	PatientName string `json:"patient_name"`
	PatientSSN  string `json:"patient_ssn"`
	CreatedAt   string `json:"created_at"`
}

func newDB() *gorm.DB {
	db, err := gorm.Open(postgres.Open(dbDSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		log.Fatalf("failed to connect to DB: %v", err)
	}
	return db
}

func (s *Server) getDenormalizedDataHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameter
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		http.Error(w, "Missing 'limit' query parameter", http.StatusBadRequest)
		return
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		http.Error(w, "Invalid 'limit' parameter - must be a positive integer", http.StatusBadRequest)
		return
	}

	// Fetch denormalized data from database
	var data []model.DenormalizedData
	result := s.db.Order("created_at DESC").Limit(limit).Find(&data)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", result.Error), http.StatusInternalServerError)
		return
	}

	// Convert to response format
	response := make([]DenormalizedDataResponse, len(data))
	for i, record := range data {
		// decode b64 state
		decodedState, err := base64.StdEncoding.DecodeString(record.State)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to decode state: %v", err), http.StatusInternalServerError)
			return
		}

		response[i] = DenormalizedDataResponse{
			ID:          record.ID,
			State:       string(decodedState),
			Diagnosis:   record.Diagnosis,
			Concern:     record.Concern,
			PatientName: record.PatientName,
			PatientSSN:  record.PatientSsn,
			CreatedAt:   record.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	// Send JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func main() {
	db := newDB()
	log.Println("Connected to database")

	server := &Server{db: db}

	http.HandleFunc("/data", server.getDenormalizedDataHandler)

	log.Printf("Starting HTTP server on %s", serverPort)
	log.Printf("Example: GET http://localhost%s/data?limit=10", serverPort)

	if err := http.ListenAndServe(serverPort, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
