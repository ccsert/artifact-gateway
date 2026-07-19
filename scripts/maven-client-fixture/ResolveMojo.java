package com.artifactgateway.fixture;

import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecutionException;

/**
 * A local test plugin whose descriptor asks Maven to resolve project compile
 * dependencies before this goal runs. This keeps the E2E client independent
 * of Maven Central's third-party dependency plugin.
 */
public final class ResolveMojo extends AbstractMojo {
    @Override
    public void execute() throws MojoExecutionException {
        getLog().info("Gateway fixture dependency resolution completed.");
    }
}
