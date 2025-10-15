//package com.example.fhirdemo.fhir;
//
//import ca.uhn.fhir.context.FhirContext;
//import ca.uhn.fhir.parser.IParser;
//import org.hl7.fhir.r4.model.OperationOutcome;
//import org.junit.jupiter.api.DisplayName;
//import org.junit.jupiter.api.Test;
//import org.springframework.beans.factory.annotation.Autowired;
//import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
//import org.springframework.boot.test.context.SpringBootTest;
//import org.springframework.http.MediaType;
//import org.springframework.test.web.servlet.MockMvc;
//import org.springframework.test.web.servlet.MvcResult;
//
//import static org.assertj.core.api.Assertions.assertThat;
//import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
//import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
//import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.content;
//import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.header;
//import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;
//
//import org.junit.jupiter.api.BeforeEach;
//import org.springframework.test.web.servlet.setup.MockMvcBuilders;
//
//class PatientControllerTests {
//
//    private static final MediaType FHIR_JSON = MediaType.parseMediaType("application/fhir+json");
//
//    MockMvc mockMvc;
//    FhirContext fhirContext;
//    IParser jsonParser;
//
//    @BeforeEach
//    void setup() {
//        FhirConfig config = new FhirConfig();
//        this.fhirContext = config.fhirContext();
//        this.jsonParser = config.jsonParser(fhirContext);
//        var validator = config.fhirValidator(fhirContext);
//        PatientValidationService service = new PatientValidationService(fhirContext, jsonParser, validator);
//        PatientController controller = new PatientController(jsonParser, service);
//        this.mockMvc = MockMvcBuilders.standaloneSetup(controller).build();
//    }
//
//    @Test
//    @DisplayName("GET /fhir/Patient/example returns a sample Patient as FHIR+JSON")
//    void examplePatient_shouldReturnSample() throws Exception {
//        mockMvc.perform(get("/fhir/Patient/example"))
//                .andExpect(status().isOk())
//                .andExpect(content().contentType(FHIR_JSON))
//                .andExpect(result -> {
//                    String body = result.getResponse().getContentAsString();
//                    var resource = jsonParser.parseResource(body);
//                    assertThat(resource.fhirType()).isEqualTo("Patient");
//                });
//    }
//
//    @Test
//    @DisplayName("POST valid Patient returns 201 Created and OperationOutcome with no errors")
//    void createPatient_success() throws Exception {
//        String patientJson = """
//        {
//          "resourceType": "Patient",
//          "active": true,
//          "name": [{"family": "Doe", "given": ["John"]}],
//          "gender": "male"
//        }
//        """;
//
//        MvcResult mvcResult = mockMvc.perform(post("/fhir/Patient")
//                        .contentType(FHIR_JSON)
//                        .content(patientJson))
//                .andExpect(status().isCreated())
//                .andExpect(content().contentType(FHIR_JSON))
//                .andExpect(header().exists("X-Operation-Outcome"))
//                .andReturn();
//
//        String body = mvcResult.getResponse().getContentAsString();
//        assertThat(jsonParser.parseResource(body).fhirType()).isEqualTo("Patient");
//
//        String outcomeHeader = mvcResult.getResponse().getHeader("X-Operation-Outcome");
//        OperationOutcome outcome = (OperationOutcome) jsonParser.parseResource(outcomeHeader);
//        boolean hasErrorOrFatal = outcome.getIssue().stream()
//                .anyMatch(i -> i.getSeverity() == OperationOutcome.IssueSeverity.ERROR
//                        || i.getSeverity() == OperationOutcome.IssueSeverity.FATAL);
//        assertThat(hasErrorOrFatal).isFalse();
//    }
//
//    @Test
//    @DisplayName("POST Patient with invalid gender returns 422 with OperationOutcome errors")
//    void createPatient_validationFailure() throws Exception {
//        String invalidPatient = """
//        {
//          "resourceType": "Patient",
//          "active": true,
//          "name": [{"family": "Doe", "given": ["Jane"]}],
//          "gender": "not-a-valid-code"
//        }
//        """;
//
//        MvcResult mvcResult = mockMvc.perform(post("/fhir/Patient")
//                        .contentType(FHIR_JSON)
//                        .content(invalidPatient))
//                .andExpect(status().isUnprocessableEntity())
//                .andExpect(content().contentType(FHIR_JSON))
//                .andExpect(header().exists("X-Operation-Outcome"))
//                .andReturn();
//
//        String body = mvcResult.getResponse().getContentAsString();
//        OperationOutcome outcomeInBody = (OperationOutcome) jsonParser.parseResource(body);
//        boolean hasErrorOrFatal = outcomeInBody.getIssue().stream()
//                .anyMatch(i -> i.getSeverity() == OperationOutcome.IssueSeverity.ERROR
//                        || i.getSeverity() == OperationOutcome.IssueSeverity.FATAL);
//        assertThat(hasErrorOrFatal).isTrue();
//
//        String outcomeHeader = mvcResult.getResponse().getHeader("X-Operation-Outcome");
//        OperationOutcome outcomeInHeader = (OperationOutcome) jsonParser.parseResource(outcomeHeader);
//        boolean headerHasErrorOrFatal = outcomeInHeader.getIssue().stream()
//                .anyMatch(i -> i.getSeverity() == OperationOutcome.IssueSeverity.ERROR
//                        || i.getSeverity() == OperationOutcome.IssueSeverity.FATAL);
//        assertThat(headerHasErrorOrFatal).isTrue();
//    }
//
//    @Test
//    @DisplayName("POST wrong resource type (Observation) returns 400 with fatal exception OperationOutcome")
//    void createPatient_parseError_wrongResourceType() throws Exception {
//        String observationJson = """
//        {
//          "resourceType": "Observation",
//          "status": "final"
//        }
//        """;
//
//        MvcResult mvcResult = mockMvc.perform(post("/fhir/Patient")
//                        .contentType(FHIR_JSON)
//                        .content(observationJson))
//                .andExpect(status().isBadRequest())
//                .andExpect(content().contentType(FHIR_JSON))
//                .andReturn();
//
//        String body = mvcResult.getResponse().getContentAsString();
//        OperationOutcome outcome = (OperationOutcome) jsonParser.parseResource(body);
//        assertThat(outcome.getIssue()).isNotEmpty();
//        assertThat(outcome.getIssueFirstRep().getSeverity()).isEqualTo(OperationOutcome.IssueSeverity.FATAL);
//        assertThat(outcome.getIssueFirstRep().getCode()).isEqualTo(OperationOutcome.IssueType.EXCEPTION);
//    }
//
//    @Test
//    @DisplayName("POST malformed JSON returns 400 with fatal exception OperationOutcome")
//    void createPatient_parseError_malformedJson() throws Exception {
//        String malformed = "{"; // invalid JSON
//
//        MvcResult mvcResult = mockMvc.perform(post("/fhir/Patient")
//                        .contentType(FHIR_JSON)
//                        .content(malformed))
//                .andExpect(status().isBadRequest())
//                .andExpect(content().contentType(FHIR_JSON))
//                .andReturn();
//
//        String body = mvcResult.getResponse().getContentAsString();
//        OperationOutcome outcome = (OperationOutcome) jsonParser.parseResource(body);
//        assertThat(outcome.getIssue()).isNotEmpty();
//        assertThat(outcome.getIssueFirstRep().getSeverity()).isEqualTo(OperationOutcome.IssueSeverity.FATAL);
//        assertThat(outcome.getIssueFirstRep().getCode()).isEqualTo(OperationOutcome.IssueType.EXCEPTION);
//    }
//}
