package com.example.fhirdemo.fhir;

import ca.uhn.fhir.context.FhirContext;
import ca.uhn.fhir.parser.IParser;
import ca.uhn.fhir.validation.FhirValidator;
import org.hl7.fhir.common.hapi.validation.support.InMemoryTerminologyServerValidationSupport;
import org.hl7.fhir.common.hapi.validation.support.ValidationSupportChain;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class FhirConfig {

    @Bean
    public FhirContext fhirContext() {
        // Use R4 context for Patient resources
        FhirContext ctx = FhirContext.forR4();
        // Be explicit about parser settings if needed
        ctx.getParserOptions().setDontStripVersionsFromReferencesAtPaths("*");
        return ctx;
    }

    @Bean
    public IParser jsonParser(FhirContext ctx) {
        // Use a lenient error handler so invalid enum codes (e.g., gender) don't throw parse errors
        var parser = ctx.newJsonParser().setPrettyPrint(true);
        ca.uhn.fhir.parser.LenientErrorHandler handler = new ca.uhn.fhir.parser.LenientErrorHandler();
        handler.setErrorOnInvalidValue(false); // downgrade invalid enum/code to a warning so validator can handle it
        parser.setParserErrorHandler(handler);
        return parser;
    }

    @Bean
    public FhirValidator fhirValidator(FhirContext ctx) {
        // Validation support chain without caffeine cache module to avoid caching dependency issues
        var baseSupport = ctx.getValidationSupport();
        InMemoryTerminologyServerValidationSupport inMemoryTx = new InMemoryTerminologyServerValidationSupport(ctx);
        ValidationSupportChain chain = new ValidationSupportChain(baseSupport, inMemoryTx);

        FhirValidator validator = ctx.newValidator();
        var instanceValidator = new org.hl7.fhir.common.hapi.validation.validator.FhirInstanceValidator(chain);
        validator.registerValidatorModule(instanceValidator);
        return validator;
    }
}
