package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"awesomeProject/internal/beclient"
	"awesomeProject/internal/fhir"
	"awesomeProject/internal/store"
)

// PatientDeps holds dependencies required by the HTTP handlers.
type PatientDeps struct {
	BE    beclient.Client
	Store store.PatientStore
}

var nextID int64 // simple counter for POST-created Patients

func (d *PatientDeps) HandleCreatePatient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2 MiB limit
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	// Quick check resourceType is Patient
	if !looksLikePatientQuick(data) {
		writeSimpleOutcome(w, http.StatusBadRequest, "invalid resourceType (expected Patient)")
		return
	}
	// Validate
	if err := fhir.ValidatePatientR4(data); err != nil {
		log.Printf("validation error: %v", err)
		writeSimpleOutcome(w, http.StatusBadRequest, err.Error())
		return
	}
	// Assign id and store
	var resource map[string]any
	if err := json.Unmarshal(data, &resource); err != nil {
		writeSimpleOutcome(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := strconv.FormatInt(atomic.AddInt64(&nextID, 1), 10)
	resource["id"] = id
	encoded, err := json.Marshal(resource)
	if err != nil {
		writeSimpleOutcome(w, http.StatusBadRequest, "failed to serialize resource")
		return
	}
	if err := d.Store.Put(id, encoded); err != nil {
		writeSimpleOutcome(w, http.StatusInternalServerError, "failed to store resource")
		return
	}
	w.Header().Set("Content-Type", "application/fhir+json")
	w.Header().Set("Location", "/fhir/Patient/"+id)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(encoded)
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
		status, body, _, err := d.BE.GetPatient(r.Context(), id, r.Header)
		if err != nil {
			log.Printf("backend request error: %v", err)
			writeSimpleOutcome(w, http.StatusBadGateway, "backend service unavailable")
			return
		}
		if status == http.StatusNotFound {
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found in backend")
			return
		}
		if status >= 200 && status < 300 {
			fhirJSON, err := fhir.TransformBackendToFHIRPatient(body, id)
			if err != nil {
				log.Printf("transform to FHIR failed: %v", err)
				writeSimpleOutcome(w, http.StatusBadGateway, "failed to transform backend response to FHIR Patient")
				return
			}
			if err := fhir.ValidatePatientR4(fhirJSON); err != nil {
				log.Printf("generated Patient failed FHIR validation: %v", err)
				writeSimpleOutcome(w, http.StatusBadGateway, "generated Patient failed FHIR R4 validation")
				return
			}
			w.Header().Set("Content-Type", "application/fhir+json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fhirJSON)
			return
		}
		// Forward non-success
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return

	case http.MethodPut:
		defer r.Body.Close()
		data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		if !looksLikePatientQuick(data) {
			writeSimpleOutcome(w, http.StatusBadRequest, "invalid resourceType (expected Patient)")
			return
		}
		if err := fhir.ValidatePatientR4(data); err != nil {
			log.Printf("validation error: %v", err)
			writeSimpleOutcome(w, http.StatusBadRequest, err.Error())
			return
		}
		if !d.Store.Exists(id) {
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found")
			return
		}
		var resource map[string]any
		if err := json.Unmarshal(data, &resource); err != nil {
			writeSimpleOutcome(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		resource["id"] = id
		encoded, err := json.Marshal(resource)
		if err != nil {
			writeSimpleOutcome(w, http.StatusBadRequest, "failed to serialize resource")
			return
		}
		if err := d.Store.Put(id, encoded); err != nil {
			writeSimpleOutcome(w, http.StatusInternalServerError, "failed to store resource")
			return
		}
		w.Header().Set("Content-Type", "application/fhir+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
		return

	case http.MethodDelete:
		if !d.Store.Delete(id) {
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

// Routes registers HTTP routes for Patient.
func Routes(deps *PatientDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fhir/Patient", deps.HandleCreatePatient)
	mux.HandleFunc("/fhir/Patient/", deps.HandlePatientByID)
	return mux
}

// Helper: minimal check on resourceType without pulling full FHIR machinery here.
func looksLikePatientQuick(data []byte) bool {
	var tmp struct{ ResourceType string `json:"resourceType"` }
	_ = json.Unmarshal(data, &tmp)
	return strings.EqualFold(tmp.ResourceType, "Patient")
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

// Convenience to satisfy interface checks at compile time
var _ = context.TODO
