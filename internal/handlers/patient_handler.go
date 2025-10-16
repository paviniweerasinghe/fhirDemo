package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"awesomeProject/internal/beclient"
	"awesomeProject/internal/fhir"
)

// PatientDeps holds dependencies required by the HTTP handlers.
type PatientDeps struct {
	BE beclient.Client
}

func (d *PatientDeps) HandlePatientByID(w http.ResponseWriter, r *http.Request) {
	prefix := "/fhir/Patient/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeSimpleOutcome(w, http.StatusBadRequest, "invalid path")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if id == "" || strings.Contains(id, "/") {
		writeSimpleOutcome(w, http.StatusBadRequest, "missing or invalid patient id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		start := time.Now()
		log.Printf("Start fetching Patient id=%s", id)
		status, body, _, err := d.BE.GetPatient(r.Context(), id, r.Header)
		if err != nil {
			log.Printf("Fetch failed (transport) id=%s err=%v duration=%s", id, err, time.Since(start))
			writeSimpleOutcome(w, http.StatusBadGateway, "backend service unavailable")
			return
		}
		if status == http.StatusNotFound {
			log.Printf("Patient not found id=%s duration=%s", id, time.Since(start))
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found in backend")
			return
		}
		if status >= 200 && status < 300 {
			log.Printf("Backend response ok id=%s status=%d bytes=%d", id, status, len(body))
			fhirJSON, err := fhir.TransformBackendToFHIRPatient(body, id)
			if err != nil {
				log.Printf("Transform to FHIR failed id=%s err=%v duration=%s", id, err, time.Since(start))
				writeSimpleOutcome(w, http.StatusBadGateway, "failed to transform backend response to FHIR Patient")
				return
			}
			if err := fhir.ValidatePatientR4(fhirJSON); err != nil {
				log.Printf("FHIR validation failed id=%s err=%v duration=%s", id, err, time.Since(start))
				writeSimpleOutcome(w, http.StatusBadGateway, "generated Patient failed FHIR R4 validation")
				return
			}
			w.Header().Set("Content-Type", "application/fhir+json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fhirJSON)
			log.Printf("Fetch success id=%s duration=%s", id, time.Since(start))
			return
		}
		// Forward non-success
		log.Printf("Backend non-success id=%s status=%d bytes=%d duration=%s", id, status, len(body), time.Since(start))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

// Routes registers HTTP routes for Patient.
func Routes(deps *PatientDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fhir/Patient/", deps.HandlePatientByID)
	return mux
}

// writeSimpleOutcome sends a minimal OperationOutcome JSON
func writeSimpleOutcome(w http.ResponseWriter, status int, diagnostics string) {
	w.Header().Set("Content-Type", "application/fhir+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resourceType": "OperationOutcome",
		"issue": []any{
			map[string]any{
				"severity":    "error",
				"code":        "invalid",
				"diagnostics": diagnostics,
			},
		},
	})
}
