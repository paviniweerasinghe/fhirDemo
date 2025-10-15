package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
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
		// If backend succeeded, transform to FHIR Patient.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fhirJSON, err := transformBackendToFHIRPatient(body, id)
			if err != nil {
				log.Printf("transform to FHIR failed: %v", err)
				writeSimpleOutcome(w, http.StatusBadGateway, "failed to transform backend response to FHIR Patient")
				return
			}
			// Validate the generated Patient against FHIR R4 before returning
			if ok, _, vErr := validatePatientR4(fhirJSON); vErr != nil || !ok {
				if vErr != nil {
					log.Printf("generated Patient failed FHIR validation: %v", vErr)
				}
				writeSimpleOutcome(w, http.StatusBadGateway, "generated Patient failed FHIR R4 validation")
				return
			}
			w.Header().Set("Content-Type", "application/fhir+json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fhirJSON)
			return
		}
		// Non-successful statuses: forward body/status as-is
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


// transformBackendToFHIRPatient transforms the backend EMPI payload to a FHIR R4 Patient JSON.
func transformBackendToFHIRPatient(beJSON []byte, pathID string) ([]byte, error) {
	// If payload is already a FHIR Patient, return as-is.
	if looksLikePatient(beJSON) {
		return beJSON, nil
	}
	// Unwrap common envelope shapes: {"details": {...}} or {"data": "<json>"} or {"data": {...}}
	var anyMap map[string]any
	if err := json.Unmarshal(beJSON, &anyMap); err != nil {
		return nil, err
	}
	payload := anyMap
	if d, ok := anyMap["details"]; ok {
		if m, ok := d.(map[string]any); ok {
			payload = m
		}
	}
	if d, ok := anyMap["data"]; ok {
		switch v := d.(type) {
		case string:
			var inner map[string]any
			if err := json.Unmarshal([]byte(v), &inner); err == nil {
				payload = inner
			}
		case map[string]any:
			payload = v
		}
	}
	// If unwrapped content itself is FHIR Patient, return it.
	if b, err := json.Marshal(payload); err == nil {
		if looksLikePatient(b) {
			return b, nil
		}
	}

	// Assemble FHIR Patient map (best-effort mapping)
	patient := map[string]any{
		"resourceType": "Patient",
		"id":           pathID,
	}
	// active
	if s := str(payload, "fileStatus"); s != "" {
		patient["active"] = strings.EqualFold(s, "active")
	}
	// identifier(s)
	identifiers := make([]any, 0, 3)
	if v := str(payload, "legacyMRN", "mrn", "medicalRecordNumber", "patientNumber"); v != "" {
		identifiers = append(identifiers, map[string]any{"system": "urn:mrn", "value": v})
	}
	if v := str(payload, "upi", "patientId", "id"); v != "" {
		identifiers = append(identifiers, map[string]any{"system": "urn:upi", "value": v})
	}
	if idType := str(payload, "idType"); idType != "" {
		if idNum := str(payload, "idNumber"); idNum != "" {
			identifiers = append(identifiers, map[string]any{"system": "urn:" + idType, "value": idNum})
		}
	}
	if len(identifiers) > 0 {
		patient["identifier"] = identifiers
	}
	// name
	first := str(payload, "firstName", "givenName")
	middle := str(payload, "middleName", "middle")
	third := str(payload, "thirdName")
	last := str(payload, "lastName", "familyName")
	full := str(payload, "fullName")
	givens := filterNonEmpty(first, middle, third)
	name := map[string]any{}
	if last != "" {
		name["family"] = last
	}
	if len(givens) > 0 {
		name["given"] = givens
	}
	if full != "" {
		name["text"] = full
	}
	if len(name) > 0 {
		patient["name"] = []any{name}
	}
	// gender
	if gtxt := str(payload, "gender_text", "sex_text"); gtxt != "" {
		patient["gender"] = normalizeGender(gtxt)
	} else if g := str(payload, "gender", "sex"); g != "" {
		patient["gender"] = normalizeGender(g)
	}
	// birthDate
	if dob := str(payload, "dateOfBirth", "dob", "birthDate"); dob != "" {
		patient["birthDate"] = normalizeDate(dob)
	}
	// deceasedBoolean
	if db, ok := boolv(payload, "isDeceased", "deceased"); ok {
		patient["deceasedBoolean"] = db
	}
	// telecom
	telecom := make([]any, 0, 2)
	if ph := str(payload, "mobileNumber", "phoneNumber", "phone"); ph != "" {
		telecom = append(telecom, map[string]any{"system": "phone", "value": ph})
	}
	if em := str(payload, "email"); em != "" {
		telecom = append(telecom, map[string]any{"system": "email", "value": em})
	}
	if len(telecom) > 0 {
		patient["telecom"] = telecom
	}
	// address
	addr := map[string]any{}
	lines := filterNonEmpty(str(payload, "street", "addressLine1"))
	if len(lines) > 0 {
		addr["line"] = lines
	}
	if city := str(payload, "city"); city != "" {
		addr["city"] = city
	}
	if state := str(payload, "state", "region", "area"); state != "" {
		addr["state"] = state
	}
	if pc := str(payload, "zipCode", "postalCode"); pc != "" {
		addr["postalCode"] = pc
	}
	if country := str(payload, "country"); country != "" {
		addr["country"] = strings.ToUpper(country)
	}
	if len(addr) > 0 {
		patient["address"] = []any{addr}
	}

	raw, err := json.Marshal(patient)
	if err != nil {
		return nil, err
	}
	// Produce canonical FHIR JSON via google/fhir and implicit validation
	canonical, err := normalizeViaGoogleFHIR(raw)
	if err != nil {
		return nil, fmt.Errorf("google/fhir normalization failed: %w", err)
	}
	return canonical, nil
}

// Helper: get first non-empty string from keys
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" && t != "-" && t != "null" {
					return t
				}
			case float64:
				return strconv.FormatInt(int64(t), 10)
			case json.Number:
				return t.String()
			}
		}
	}
	return ""
}

func boolv(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case bool:
				return t, true
			case string:
				lower := strings.ToLower(strings.TrimSpace(t))
				if lower == "true" || lower == "1" || lower == "yes" {
					return true, true
				}
				if lower == "false" || lower == "0" || lower == "no" {
					return false, true
				}
			case float64:
				return t != 0, true
			}
		}
	}
	return false, false
}

func filterNonEmpty(vals ...string) []string {
	res := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" && v != "-" && v != "null" {
			res = append(res, v)
		}
	}
	return res
}

func normalizeGender(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	switch g {
	case "m", "male", "1":
		return "male"
	case "f", "female", "2":
		return "female"
	case "other", "o", "3":
		return "other"
	case "unknown", "u", "0":
		return "unknown"
	default:
		if strings.HasPrefix(g, "m") {
			return "male"
		}
		if strings.HasPrefix(g, "f") {
			return "female"
		}
		return "unknown"
	}
}

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "T "); i > 0 {
		s = s[:i]
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// normalizeViaGoogleFHIR validates the generated Patient JSON via google/fhir (R4)
// by unmarshalling to the typed model. If valid, it returns the input unchanged.
func normalizeViaGoogleFHIR(patientJSON []byte) ([]byte, error) {
	um, err := jsonformat.NewUnmarshaller("UTC", fhirversion.R4)
	if err != nil {
		return nil, err
	}
	if _, err := um.Unmarshal(patientJSON); err != nil {
		return nil, err
	}
	return patientJSON, nil
}
