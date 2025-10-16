package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
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

// HandlePatientSearch implements GET /fhir/Patient search returning a FHIR Bundle of Patients.
func (d *PatientDeps) HandlePatientSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/fhir/Patient" {
		writeSimpleOutcome(w, http.StatusBadRequest, "invalid path")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	q := r.URL.Query()
	filters := buildSearchFilters(q)
	log.Printf("Start searching Patient filters=%v", filters)
	// Pagination defaults similar to BE example
	status, body, _, err := d.BE.SearchPatients(r.Context(), filters, 0, 10, r.Header)
	if err != nil {
		log.Printf("Search failed (transport) err=%v duration=%s", err, time.Since(start))
		writeSimpleOutcome(w, http.StatusBadGateway, "backend service unavailable")
		return
	}
	if status < 200 || status >= 300 {
		log.Printf("Search backend non-success status=%d bytes=%d duration=%s", status, len(body), time.Since(start))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	// Expect backend to return a page payload. Try to extract the list safely.
	var anyMap map[string]any
	if err := json.Unmarshal(body, &anyMap); err != nil {
		log.Printf("Search backend payload invalid JSON err=%v duration=%s", err, time.Since(start))
		writeSimpleOutcome(w, http.StatusBadGateway, "invalid backend search response")
		return
	}
	items := extractItems(anyMap)
	entries := make([]any, 0, len(items))
	for _, item := range items {
		b, _ := json.Marshal(item)
		// Try to derive an id for pathID: prefer item.id or upi
		pid := ""
		if m, ok := item.(map[string]any); ok {
			if v, ok := m["id"].(string); ok {
				pid = v
			}
			if pid == "" {
				if v, ok := m["upi"].(string); ok {
					pid = v
				}
			}
		}
		patJSON, err := fhir.TransformBackendToFHIRPatient(b, pid)
		if err != nil {
			continue // skip bad items
		}
		if err := fhir.ValidatePatientR4(patJSON); err != nil {
			// still include unvalidated? choose to skip to keep Bundle valid
			continue
		}
		var pat map[string]any
		if err := json.Unmarshal(patJSON, &pat); err != nil {
			continue
		}
		entries = append(entries, map[string]any{
			"fullUrl":  "urn:uuid:" + randomUUIDLike(pid),
			"resource": pat,
			"search":   map[string]any{"mode": "match"},
		})
	}
	// Determine total from backend if provided (falls back to number of included entries)
	total := len(entries)
	if v, ok := anyMap["totalRows"]; ok {
		switch t := v.(type) {
		case float64:
			total = int(t)
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				total = n
			}
		}
	}
	bundle := map[string]any{
		"resourceType": "Bundle",
		"type":         "searchset",
		"total":        total,
		"entry":        entries,
	}
	w.Header().Set("Content-Type", "application/fhir+json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(bundle)
	log.Printf("Search success entries=%d total=%d duration=%s", len(entries), total, time.Since(start))
}

func deriveNamesFromQuery(q url.Values) (firstName, lastName string) {
	// Accept both FHIR-style (given/family/name) and direct (firstName/lastName)
	if v := q.Get("firstName"); v != "" {
		firstName = v
	}
	if v := q.Get("lastName"); v != "" {
		lastName = v
	}
	if v := q.Get("given"); v != "" && firstName == "" {
		firstName = v
	}
	if v := q.Get("family"); v != "" && lastName == "" {
		lastName = v
	}
	if v := q.Get("name"); v != "" {
		parts := strings.Fields(v)
		if len(parts) == 1 {
			if firstName == "" {
				firstName = parts[0]
			} else if lastName == "" {
				lastName = parts[0]
			}
		} else if len(parts) >= 2 {
			if firstName == "" {
				firstName = parts[0]
			}
			if lastName == "" {
				lastName = parts[len(parts)-1]
			}
		}
	}
	return
}

// buildSearchFilters collects supported search fields and maps them to backend keys.
func buildSearchFilters(q url.Values) map[string]string {
	filters := make(map[string]string)
	fn, ln := deriveNamesFromQuery(q)
	if fn != "" {
		filters["firstName"] = fn
	}
	if ln != "" {
		filters["lastName"] = ln
	}
	// Direct pass-through fields supported by BE
	if v := q.Get("upi"); v != "" {
		filters["upi"] = v
	}
	if v := q.Get("idNumber"); v != "" {
		filters["idNumber"] = v
	}
	if v := q.Get("dateOfBirth"); v != "" {
		filters["dateOfBirth"] = v
	}
	// Keys with dots are acceptable as URL query keys; pass them as-is
	if v := q.Get("localMRNs.59"); v != "" {
		filters["localMRNs.59"] = v
	}
	if v := q.Get("legacyMRNs.59"); v != "" {
		filters["legacyMRNs.59"] = v
	}
	return filters
}

func extractItems(m map[string]any) []any {
	// Try common shapes: {"data": {"rows": [...]}} or {"rows": [...]} or {"data": [...]}
	if d, ok := m["data"]; ok {
		switch v := d.(type) {
		case map[string]any:
			if rows, ok := v["rows"].([]any); ok {
				return rows
			}
			if list, ok := v["list"].([]any); ok {
				return list
			}
		case []any:
			return v
		}
	}
	if rows, ok := m["rows"].([]any); ok {
		return rows
	}
	if list, ok := m["list"].([]any); ok {
		return list
	}
	return []any{}
}

func randomUUIDLike(s string) string {
	// Not a true UUID; just ensure fullUrl uniqueness for demo purposes.
	if s == "" {
		s = "x"
	}
	return s + "-" + time.Now().UTC().Format("20060102150405.000000000")
}

// Routes registers HTTP routes for Patient.
func Routes(deps *PatientDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fhir/Patient", deps.HandlePatientSearch)
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
