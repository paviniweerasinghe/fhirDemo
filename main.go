package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	fhirversion "github.com/google/fhir/go/fhirversion"
	jsonformat "github.com/google/fhir/go/jsonformat"
)

// Minimal FHIR proxy: supports creating, retrieving, updating, and deleting a Patient and validates it with google/fhir jsonformat.
// Endpoints:
//
//  POST   /fhir/Patient            (body: FHIR R4 Patient JSON)
//  GET    /fhir/Patient/{id}
//  PUT    /fhir/Patient/{id}       (body: full Patient JSON)
//  DELETE /fhir/Patient/{id}
//
// Responses:
//
//  201 Created with echoed resource on success (POST)
//  200 OK with stored resource on success (GET/PUT)
//  204 No Content on successful delete (DELETE)
//  400 Bad Request with OperationOutcome JSON on validation errors
//  404 Not Found with OperationOutcome JSON when id not found

// In-memory storage (ephemeral) for created Patients.
var (
	patientStore   = make(map[string][]byte)
	patientStoreMu sync.RWMutex
	nextID         int64
)

func main() {
	http.HandleFunc("/fhir/Patient", handleCreatePatient)
	http.HandleFunc("/fhir/Patient/", handlePatientByID)
	log.Println("FHIR proxy listening on :8080 (POST /fhir/Patient, GET/PUT/DELETE /fhir/Patient/{id})")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func handleCreatePatient(w http.ResponseWriter, r *http.Request) {
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

	// Quick check resourceType is Patient to avoid obviously wrong inputs
	if !looksLikePatient(data) {
		writeSimpleOutcome(w, http.StatusBadRequest, "invalid resourceType (expected Patient)")
		return
	}

	// Validate using google/fhir jsonformat against R4 core definitions.
	ok, outcomeJSON, err := validatePatientR4(data)
	if err != nil {
		log.Printf("validation error: %v", err)
		writeSimpleOutcome(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		w.Header().Set("Content-Type", "application/fhir+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(outcomeJSON)
		return
	}

	// Assign an ID, set it into the Patient, store, and return 201 with Location
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
	patientStoreMu.Lock()
	patientStore[id] = encoded
	patientStoreMu.Unlock()

	w.Header().Set("Content-Type", "application/fhir+json")
	w.Header().Set("Location", "/fhir/Patient/"+id)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(encoded)
}

// validatePatientR4 attempts to unmarshal+validate the input as an R4 Patient using jsonformat.
// It returns ok=true if validation passes. If validation produces an OperationOutcome with issues,
// ok=false and outcomeJSON contains the serialized OperationOutcome.
func validatePatientR4(data []byte) (ok bool, outcomeJSON []byte, err error) {
	// google/fhir v0.7.x API: construct unmarshaller with timezone and version.
	um, err := jsonformat.NewUnmarshaller("UTC", fhirversion.R4)
	if err != nil {
		return false, nil, err
	}
	// Unmarshal+validate. Validation errors are returned as error; no OperationOutcome is produced here.
	if _, err := um.Unmarshal(data); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

// looksLikePatient does a minimal check to see if resourceType is "Patient" in the JSON.
func looksLikePatient(data []byte) bool {
	var tmp struct {
		ResourceType string `json:"resourceType"`
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return false
	}
	return strings.EqualFold(tmp.ResourceType, "Patient")
}

// writeSimpleOutcome sends a minimal OperationOutcome-like JSON when full proto outcome isn't available.
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

// handlePatientByID serves GET and PUT for /fhir/Patient/{id}
func handlePatientByID(w http.ResponseWriter, r *http.Request) {
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
		// Proxy the GET to the external BE endpoint instead of in-memory store
		beURL := "https://dev.cloudsolutions.com.sa/csi-api/csi-net-empiread/api/patient/" + id + "?includeClosed=true"
		// Custom transport to mirror curl -k (insecure TLS) for dev environment
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
		req, err := http.NewRequest(http.MethodGet, beURL, nil)
		if err != nil {
			writeSimpleOutcome(w, http.StatusBadGateway, "failed to build backend request")
			return
		}
		// Forward important headers; use sensible defaults if missing to match provided curl
		accept := r.Header.Get("Accept")
		if accept == "" {
			accept = "application/json, text/plain, */*"
		}
		req.Header.Set("Accept", accept)
		if v := r.Header.Get("Accept-Language"); v != "" {
			req.Header.Set("Accept-Language", v)
		}
		if v := r.Header.Get("Referer"); v != "" {
			req.Header.Set("Referer", v)
		}
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
		}
		req.Header.Set("User-Agent", ua)
		// Required X-* headers for BE
		setOrDefault := func(name, def string) {
			if v := r.Header.Get(name); v != "" {
				req.Header.Set(name, v)
			} else if def != "" {
				req.Header.Set(name, def)
			}
		}
		setOrDefault("X-Group", "58")
		setOrDefault("X-Hospital", "59")
		setOrDefault("X-Location", "59")
		setOrDefault("X-Module", "empi")
		setOrDefault("X-User", "8008")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("backend request error: %v", err)
			writeSimpleOutcome(w, http.StatusBadGateway, "backend service unavailable")
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			writeSimpleOutcome(w, http.StatusBadGateway, "failed to read backend response")
			return
		}
		// Pass through status code. On 404, map to OperationOutcome for consistency.
		if resp.StatusCode == http.StatusNotFound {
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found in backend")
			return
		}
		// Forward body with JSON content type by default.
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return

	case http.MethodPut:
		defer r.Body.Close()
		data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		if !looksLikePatient(data) {
			writeSimpleOutcome(w, http.StatusBadRequest, "invalid resourceType (expected Patient)")
			return
		}
		if ok, _, err := validatePatientR4(data); err != nil || !ok {
			if err != nil {
				log.Printf("validation error: %v", err)
				writeSimpleOutcome(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		// Confirm patient exists to update
		patientStoreMu.RLock()
		_, exists := patientStore[id]
		patientStoreMu.RUnlock()
		if !exists {
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found")
			return
		}
		// Ensure resource id matches path id (set/overwrite)
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
		patientStoreMu.Lock()
		patientStore[id] = encoded
		patientStoreMu.Unlock()
		w.Header().Set("Content-Type", "application/fhir+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
		return

	case http.MethodDelete:
		patientStoreMu.Lock()
		if _, ok := patientStore[id]; !ok {
			patientStoreMu.Unlock()
			writeSimpleOutcome(w, http.StatusNotFound, "Patient not found")
			return
		}
		delete(patientStore, id)
		patientStoreMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}
