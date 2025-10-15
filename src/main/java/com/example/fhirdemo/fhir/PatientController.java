package com.example.fhirdemo.fhir;

import ca.uhn.fhir.parser.IParser;
import org.hl7.fhir.r4.model.Patient;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/fhir")
public class PatientController {

    private final IParser jsonParser;
    private final PatientValidationService validationService;

    public PatientController(IParser jsonParser, PatientValidationService validationService) {
        this.jsonParser = jsonParser;
        this.validationService = validationService;
    }

    // Example Patient to help clients
    @GetMapping(value = "/Patient/example", produces = "application/fhir+json")
    public ResponseEntity<String> examplePatient() {
        Patient p = new Patient();
        p.addName().setFamily("Doe").addGiven("John");
        p.setGender(org.hl7.fhir.r4.model.Enumerations.AdministrativeGender.MALE);
        p.setActive(true);
        String body = jsonParser.encodeResourceToString(p);
        return ResponseEntity.ok()
                .contentType(MediaType.parseMediaType("application/fhir+json"))
                .body(body);
    }

    // Create (proxy) Patient: validate and echo back, returning OperationOutcome status
    @PostMapping(value = "/Patient", consumes = {"application/fhir+json", MediaType.APPLICATION_JSON_VALUE}, produces = "application/fhir+json")
    public ResponseEntity<String> createPatient(@RequestBody String patientJson) {
        return validationService.processCreatePatient(patientJson);
    }
}
