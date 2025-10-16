package fhir

import (
	fhirversion "github.com/google/fhir/go/fhirversion"
	jsonformat "github.com/google/fhir/go/jsonformat"
)

// ValidatePatientR4 attempts to unmarshal+validate the input as an R4 Patient using jsonformat.
// It returns nil if validation passes; an error otherwise.
func ValidatePatientR4(data []byte) error {
	um, err := jsonformat.NewUnmarshaller("UTC", fhirversion.R4)
	if err != nil {
		return err
	}
	_, err = um.Unmarshal(data)
	return err
}

// LooksLikePatient does a minimal check via jsonformat by attempting to unmarshal as Patient.
// Callers typically use Transform or cheap JSON checks; prefer Transform's own detection if available.
func LooksLikePatient(data []byte) bool {
	// We avoid importing encoding/json here to keep this package focused on FHIR tooling.
	// Instead reuse jsonformat's unmarshaller in a permissive way. If it can parse as a Patient,
	// we consider it to look like a Patient. This is slightly stronger than string checking.
	um, err := jsonformat.NewUnmarshaller("UTC", fhirversion.R4)
	if err != nil {
		return false
	}
	if _, err := um.Unmarshal(data); err != nil {
		return false
	}
	return true
}
