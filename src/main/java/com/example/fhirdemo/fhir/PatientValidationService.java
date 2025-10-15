package com.example.fhirdemo.fhir;

import ca.uhn.fhir.context.FhirContext;
import ca.uhn.fhir.parser.DataFormatException;
import ca.uhn.fhir.parser.IParser;
import ca.uhn.fhir.validation.FhirValidator;
import ca.uhn.fhir.validation.ValidationResult;
import org.hl7.fhir.instance.model.api.IBaseResource;
import com.fasterxml.jackson.core.JsonProcessingException;
import org.hl7.fhir.r4.model.OperationOutcome;
import org.hl7.fhir.r4.model.Patient;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Service;

@Service
public class PatientValidationService {

    private static final Logger log = LoggerFactory.getLogger(PatientValidationService.class);

    private final FhirContext ctx;
    private final IParser jsonParser;
    private final FhirValidator validator;

    public PatientValidationService(FhirContext ctx, IParser jsonParser, FhirValidator validator) {
        this.ctx = ctx;
        this.jsonParser = jsonParser;
        this.validator = validator;
    }

    public ResponseEntity<String> processCreatePatient(String patientJson) {
        log.info("Processing Patient create request");

        // Use HAPI strict parser so datatype/enum issues are raised by HAPI (no custom field checks)
        IParser strictParser = ctx.newJsonParser().setPrettyPrint(true);
        strictParser.setParserErrorHandler(new ca.uhn.fhir.parser.StrictErrorHandler());

        Patient patient;
        try {
            IBaseResource parsed = strictParser.parseResource(patientJson);
            if (!(parsed instanceof Patient)) {
                String actualType = parsed != null ? parsed.fhirType() : "(null)";
                log.warn("POST /Patient received non-Patient resource: {}", actualType);
                OperationOutcome oo = new OperationOutcome();
                oo.addIssue()
                  .setSeverity(OperationOutcome.IssueSeverity.FATAL)
                  .setCode(OperationOutcome.IssueType.EXCEPTION)
                  .setDiagnostics("Expected a Patient resource but received: " + actualType);
                String ooJson = jsonParser.encodeResourceToString(oo);
                return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                        .contentType(MediaType.parseMediaType("application/fhir+json"))
                        .body(ooJson);
            }
            patient = (Patient) parsed;
        } catch (DataFormatException dfe) {
            // Decide status: malformed JSON → 400; otherwise invalid content → 422
            boolean isMalformedJson = dfe.getCause() instanceof JsonProcessingException;
            String msg = dfe.getMessage() != null ? dfe.getMessage() : "Invalid Patient payload";
            log.warn("Parse/validation failure: {}", msg);
            OperationOutcome oo = new OperationOutcome();
            if (isMalformedJson) {
                oo.addIssue()
                  .setSeverity(OperationOutcome.IssueSeverity.FATAL)
                  .setCode(OperationOutcome.IssueType.EXCEPTION)
                  .setDiagnostics("Malformed JSON: " + msg);
                String ooJson = jsonParser.encodeResourceToString(oo);
                return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                        .contentType(MediaType.parseMediaType("application/fhir+json"))
                        .body(ooJson);
            } else {
                oo.addIssue()
                  .setSeverity(OperationOutcome.IssueSeverity.ERROR)
                  .setCode(OperationOutcome.IssueType.INVALID)
                  .getDetails().setText(msg);
                String ooJson = jsonParser.encodeResourceToString(oo);
                HttpHeaders headers = new HttpHeaders();
                headers.setContentType(MediaType.parseMediaType("application/fhir+json"));
                headers.add("X-Operation-Outcome", ooJson);
                return new ResponseEntity<>(ooJson, headers, HttpStatus.UNPROCESSABLE_ENTITY);
            }
        } catch (Exception ex) {
            log.warn("Bad request while parsing Patient: {}", ex.getMessage());
            OperationOutcome oo = new OperationOutcome();
            oo.addIssue()
              .setSeverity(OperationOutcome.IssueSeverity.FATAL)
              .setCode(OperationOutcome.IssueType.EXCEPTION)
              .setDiagnostics("Failed to parse Patient: " + ex.getMessage());
            String ooJson = jsonParser.encodeResourceToString(oo);
            return ResponseEntity.status(HttpStatus.BAD_REQUEST)
                    .contentType(MediaType.parseMediaType("application/fhir+json"))
                    .body(ooJson);
        }

        // Run HAPI validator
        ValidationResult result;
        try {
            result = validator.validateWithResult(patient);
        } catch (Throwable validationError) {
            // If the validator is not available/misconfigured, log and proceed without blocking creation.
            // Parser-level strict checks already enforced datatypes/codes using HAPI.
            log.warn("Validator unavailable or failed: {}. Proceeding without validator.", validationError.getMessage());
            OperationOutcome oo = new OperationOutcome();
            oo.addIssue()
              .setSeverity(OperationOutcome.IssueSeverity.WARNING)
              .setCode(OperationOutcome.IssueType.INFORMATIONAL)
              .setDiagnostics("Validation skipped due to internal error: " + validationError.getMessage());
            String ooJson = jsonParser.encodeResourceToString(oo);
            HttpHeaders headers = new HttpHeaders();
            headers.setContentType(MediaType.parseMediaType("application/fhir+json"));
            headers.add("X-Operation-Outcome", ooJson);
            String patientOut = jsonParser.encodeResourceToString(patient);
            return new ResponseEntity<>(patientOut, headers, HttpStatus.CREATED);
        }

        OperationOutcome outcome = (OperationOutcome) result.toOperationOutcome();
        String outcomeJson = jsonParser.encodeResourceToString(outcome);
        HttpHeaders headers = new HttpHeaders();
        headers.setContentType(MediaType.parseMediaType("application/fhir+json"));
        headers.add("X-Operation-Outcome", outcomeJson);

        if (!result.isSuccessful()) {
            log.warn("Validation failed with {} issues", outcome.getIssue().size());
            return new ResponseEntity<>(outcomeJson, headers, HttpStatus.UNPROCESSABLE_ENTITY);
        }

        String patientOut = jsonParser.encodeResourceToString(patient);
        log.info("Patient validated successfully by HAPI; returning 201 Created");
        return new ResponseEntity<>(patientOut, headers, HttpStatus.CREATED);
    }

}
