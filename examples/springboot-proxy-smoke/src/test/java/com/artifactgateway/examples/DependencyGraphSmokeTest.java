package com.artifactgateway.examples;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;
import org.springframework.boot.SpringApplication;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.testcontainers.containers.PostgreSQLContainer;

class DependencyGraphSmokeTest {
    @Test
    void representativeDependenciesAreOnTheTestClasspath() {
        assertThat(SpringApplication.class).isNotNull();
        assertThat(new BCryptPasswordEncoder().encode("gateway")).startsWith("$2");
        assertThat(PostgreSQLContainer.class.getName()).contains("PostgreSQL");
    }
}
