package main

import (
	"log"
	"net/http"
	"time"

	"awesomeProject/internal/api"
	"awesomeProject/internal/beclient"
	"awesomeProject/internal/store"
)

// Minimal FHIR proxy wiring: delegates HTTP handling to internal packages and keeps main thin.
func main() {
	// Dependencies
	be := beclient.NewHTTPClient(
		"https://dev.cloudsolutions.com.sa/csi-api/csi-net-empiread/api/patient",
		15*time.Second,
		true, // insecure TLS for dev, mirrors curl -k
	)
	st := store.NewMem()
	deps := &api.PatientDeps{BE: be, Store: st}

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      api.Routes(deps),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Println("FHIR proxy listening on :8080 (POST /fhir/Patient, GET/PUT/DELETE /fhir/Patient/{id})")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
