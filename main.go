package main

import (
	"log"
	"net/http"
	"time"

	"awesomeProject/internal/beclient"
	"awesomeProject/internal/handlers"
)

func main() {
	be := beclient.NewHTTPClient(
		"https://dev.cloudsolutions.com.sa/csi-api/csi-net-empiread/api/patient",
		15*time.Second,
		true, // insecure TLS for dev, mirrors curl -k
	)
	deps := &handlers.PatientDeps{BE: be}

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      handlers.Routes(deps),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Println("FHIR proxy listening on :8080 (GET /fhir/Patient/{id})")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
