package fhir

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	fhirversion "github.com/google/fhir/go/fhirversion"
	jsonformat "github.com/google/fhir/go/jsonformat"
)

// TransformBackendToFHIRPatient transforms the backend EMPI payload into a FHIR R4 Patient JSON.
// pathID is used to set/override the Patient.id.
func TransformBackendToFHIRPatient(beJSON []byte, pathID string) ([]byte, error) {
	// If payload is already a FHIR Patient, return as-is.
	if LooksLikePatient(beJSON) {
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
		if LooksLikePatient(b) {
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
	if v := str(payload, "legacyMRN", "medicalRecordNumber", "patientNumber"); v != "" {
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
	if gtxt := str(payload, "gender_text"); gtxt != "" {
		patient["gender"] = normalizeGender(gtxt)
	} else if g := str(payload, "gender"); g != "" {
		patient["gender"] = normalizeGender(g)
	}
	// birthDate
	if dob := str(payload, "dateOfBirth"); dob != "" {
		patient["birthDate"] = normalizeDate(dob)
	}
	// maritalStatus: return the raw BE value (e.g., "2") as text only
	if ms := str(payload, "maritialStatus", "maritalStatus"); ms != "" {
		patient["maritalStatus"] = map[string]any{
			"text": ms,
		}
	}
	// communication: show raw BE 'language' value as text (no code mapping yet)
	if lang := str(payload, "language"); lang != "" {
		patient["communication"] = []any{
			map[string]any{
				"language": map[string]any{"text": lang},
			},
		}
	}
	// deceasedBoolean
	if db, ok := boolv(payload, "isDeceased"); ok {
		patient["deceasedBoolean"] = db
	}
	// telecom
	telecom := make([]any, 0, 2)
	if ph := str(payload, "mobileNumber", "phoneNumber"); ph != "" {
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
	lines := filterNonEmpty(str(payload, "street"))
	if len(lines) > 0 {
		addr["line"] = lines
	}
	if city := str(payload, "city"); city != "" {
		addr["city"] = city
	}
	if state := str(payload, "area"); state != "" {
		addr["state"] = state
	}
	if pc := str(payload, "zipCode"); pc != "" {
		addr["postalCode"] = pc
	}
	if country := str(payload, "country"); country != "" {
		addr["country"] = strings.ToUpper(country)
	}
	if len(addr) > 0 {
		patient["address"] = []any{addr}
	}
	// managingOrganization: prefer registeredAt, else hospitalId
	if orgID := str(payload, "registeredAt", "hospitalId"); orgID != "" {
		patient["managingOrganization"] = map[string]any{
			"reference": "Organization/" + orgID,
		}
	}
	// generalPractitioner
	gp := make([]any, 0, 2)
	if pid := str(payload, "primaryHealthcarePhysician"); pid != "" {
		gp = append(gp, map[string]any{"reference": "Practitioner/" + pid})
	}
	if cid := str(payload, "primaryHealthcareCenter"); cid != "" {
		gp = append(gp, map[string]any{"reference": "Organization/" + cid})
	}
	if len(gp) > 0 {
		patient["generalPractitioner"] = gp
	}
	// link
	if parent := str(payload, "linkedParentUpi"); parent != "" {
		patient["link"] = []any{map[string]any{
			"other": map[string]any{"reference": "Patient/" + parent},
			"type":  "seealso",
		}}
	}
	// contact (emergency only)
	contacts := make([]any, 0, 1)
	{
		nameText := str(payload, "emergencyContactName")
		first := str(payload, "emergencyContactFirstName", "emergencyContactFirstNameLocal")
		last := str(payload, "emergencyContactLastName", "emergencyContactLastNameLocal")
		name := map[string]any{}
		if nameText != "" { name["text"] = nameText }
		givens := filterNonEmpty(first)
		if len(givens) > 0 { name["given"] = givens }
		if last != "" { name["family"] = last }
		telecom := make([]any, 0, 2)
		if ph := str(payload, "emergencyContactPhoneNumber"); ph != "" {
			telecom = append(telecom, map[string]any{"system": "phone", "value": ph})
		}
		if em := str(payload, "emergencyContactEmail"); em != "" {
			telecom = append(telecom, map[string]any{"system": "email", "value": em})
		}
		relText := str(payload, "emergencyContactRelationship")
		contact := map[string]any{}
		if len(name) > 0 { contact["name"] = name }
		if relText != "" { contact["relationship"] = []any{map[string]any{"text": relText}} }
		if len(telecom) > 0 { contact["telecom"] = telecom }
		if len(contact) > 0 { contacts = append(contacts, contact) }
	}
	if len(contacts) > 0 { patient["contact"] = contacts }
	// photo
	attachments := make([]any, 0, 1)
	if u := str(payload, "photoUrl", "avatarUrl", "imageUrl", "pictureUrl"); u != "" {
		att := map[string]any{"url": u}
		if ct := str(payload, "photoContentType", "imageContentType", "contentType"); ct != "" {
			att["contentType"] = ct
		} else if guessed := guessImageContentType(u); guessed != "" {
			att["contentType"] = guessed
		}
		if title := str(payload, "photoTitle"); title != "" { att["title"] = title }
		if created := str(payload, "photoCreatedOn", "photoCreation", "createdOn", "modifiedOn"); created != "" {
			att["creation"] = created
		}
		attachments = append(attachments, att)
	} else if b64 := str(payload, "photoBase64", "avatarBase64", "imageBase64", "imageData", "photo"); b64 != "" {
		att := map[string]any{"data": b64}
		if ct := str(payload, "photoContentType", "imageContentType", "contentType"); ct != "" {
			att["contentType"] = ct
		}
		if title := str(payload, "photoTitle"); title != "" { att["title"] = title }
		if created := str(payload, "photoCreatedOn", "photoCreation", "createdOn", "modifiedOn"); created != "" {
			att["creation"] = created
		}
		attachments = append(attachments, att)
	}
	if len(attachments) > 0 { patient["photo"] = attachments }

	raw, err := json.Marshal(patient)
	if err != nil { return nil, err }
	canonical, err := normalizeViaGoogleFHIR(raw)
	if err != nil {
		return nil, fmt.Errorf("google/fhir normalization failed: %w", err)
	}
	return canonical, nil
}

// normalizeViaGoogleFHIR validates the generated Patient JSON via google/fhir (R4)
// by unmarshalling to the typed model. If valid, it returns the input unchanged.
func normalizeViaGoogleFHIR(patientJSON []byte) ([]byte, error) {
	um, err := jsonformat.NewUnmarshaller("UTC", fhirversion.R4)
	if err != nil { return nil, err }
	if _, err := um.Unmarshal(patientJSON); err != nil { return nil, err }
	return patientJSON, nil
}

// Helpers
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" && t != "-" && t != "null" { return t }
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
				if lower == "true" || lower == "1" || lower == "yes" { return true, true }
				if lower == "false" || lower == "0" || lower == "no" { return false, true }
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
		if v != "" && v != "-" && v != "null" { res = append(res, v) }
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
		if strings.HasPrefix(g, "m") { return "male" }
		if strings.HasPrefix(g, "f") { return "female" }
		return "unknown"
	}
}

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "T "); i > 0 { s = s[:i] }
	if len(s) >= 10 { return s[:10] }
	return s
}

func guessImageContentType(u string) string {
	lower := strings.ToLower(u)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		return ""
	}
}

// HTTPTransport returns an HTTP client transport configured for insecure TLS (curl -k) when needed.
// Provided here in case callers in other packages want a ready-to-use transport.
func HTTPTransport(insecure bool) *http.Transport {
	if insecure {
		return &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return http.DefaultTransport.(*http.Transport)
}
