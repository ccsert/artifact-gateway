# Nexus Repository Maven Hosted Publication Research

[简体中文](nexus-maven-publication-research.zh-CN.md) | [Documentation index](README.md)

Research date: 2026-08-21.

Scope: ordinary Sonatype Nexus Repository 3 Maven Hosted, Nexus Repository Pro
Staging, and standard Maven/Gradle publishing clients. Evidence is limited to
official Sonatype documentation and source, Apache Maven documentation, and
Gradle documentation.

## Executive conclusion

1. Ordinary Nexus Maven Hosted does not require a finalize or commit call.
   Official configuration sends `mvn clean deploy` directly to the target
   Hosted Repository.
2. Each successful PUT becomes readable as an individual asset. Nexus saves
   the path during PUT, returns `201 Created`, and GET/HEAD reads that path.
3. Ordinary Hosted does not provide coordinate-level atomic publication.
   Maven and Gradle upload POMs, primary artifacts, classifiers, checksums, and
   metadata in independent requests; a mid-publish failure can leave already
   successful assets visible.
4. Nexus Repository Pro Staging is a separate layer. It uses Hosted
   Repositories, component tags, and move/delete APIs to promote a build
   between environments; it is not a hidden coordinate commit after ordinary
   Maven PUTs.
5. Nexus Repository 3 Staging must not be confused with Nexus Repository 2's
   classic close/release flow. Nexus 2 created a repository per build; Nexus 3
   uses a tag per build and moves content between a fixed set of Hosted repos.
6. Artifact Gateway therefore uses direct publication by default and keeps
   `mavenStrictPublication=true` as an explicit opt-in.

## 1. Does ordinary `mvn deploy` need another commit?

No. Sonatype's [Maven Repositories](https://help.sonatype.com/en/maven-repositories.html)
configures the Hosted URL in `distributionManagement` and presents
`mvn clean deploy` as the complete build and upload command. It does not add a
finalize, close, or commit request.

The [Maven Deploy Plugin](https://maven.apache.org/plugins/maven-deploy-plugin/)
publishes the primary artifact, POM, attached artifacts, metadata, and
checksums through independent repository-layout requests. `deployAtEnd` may
delay a multi-module reactor until the build reaches deployment, but it does
not define a server transaction after requests have started.

Gradle's [`maven-publish`](https://docs.gradle.org/current/userguide/publishing_maven.html)
uses the same standards and lists Nexus as a compatible target. It has no
Nexus-specific finalize step.

```text
mvn deploy / gradle publish
        ↓
independent POM, JAR, classifier, checksum, and metadata uploads
        ↓
each successful request enters the target Hosted Repository
        ↓
the client task succeeds after all of its requests succeed
```

The final line is client task completion, not a protocol message saying that
the coordinate is complete.

## 2. Is an asset visible after each PUT?

Yes. In Nexus Repository 3.93.2,
[`MavenContentHandler`](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentHandler.java)
calls `storage.put(mavenPath, payload)` and returns `201`; GET/HEAD calls
`storage.get(mavenPath)` and returns the found asset. No staging status or
published flag sits between those paths.

[`MavenContentFacetImpl`](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentFacetImpl.java)
ingests the blob, validates Maven metadata, creates or locates a component, and
saves the current asset. Therefore a successful POM can remain readable if a
later JAR fails, and the inverse is also possible. A component row created by
the first coordinate-bearing asset does not prove every expected file exists.

Here, “immediately visible” means a later GET to the same Hosted path can read
the asset after PUT succeeds. It does not claim immediate replication, proxy
cache, or search-index visibility.

## 3. Does ordinary Hosted offer coordinate-level atomicity?

No public protocol or implementation guarantee was found. Maven repository
objects are separate paths such as POM, JAR, sources, checksum, and
`maven-metadata.xml`. Maven deploy uploads them individually and Nexus stores
each PUT. Neither side defines an expected-asset declaration followed by a
single GAV visibility switch.

Nexus [Deployment Policy](https://help.sonatype.com/en/configurable-repository-fields.html#deployment-policy)
controls redeploy, overwrite, or read-only behavior; it is not a coordinate
completeness transaction.

The trade-off is zero-change client compatibility versus partial-coordinate
risk when a client or network fails between requests. Typical mitigations are
client order, `deployAtEnd`, disabling release redeploy, CI checks, and isolated
repositories with Staging.

## 4. Nexus Repository Pro Staging

Current Nexus 3 [Staging](https://help.sonatype.com/en/staging.html) combines:

1. Hosted Repositories that receive ordinary uploads;
2. component tags that identify a build group;
3. REST endpoints that move or delete matching components.

```text
maven-dev-hosted
    ↑ ordinary uploads, optionally tagged
    ├─ CI passes ── move API ──→ maven-uat-hosted ──→ maven-prod-hosted
    └─ CI fails ── delete API
```

Consumers normally read Groups associated with lifecycle stages. A partial
build may be visible inside the development Hosted Repository without becoming
visible in the production Group.

The Nexus 3 `nxrm3-maven-plugin` can assign a tag while uploading and provides
`staging-deploy`, `staging-move`, and `staging-delete`. This is build identity,
environment isolation, and promotion, not a hidden per-GAV commit. Public
documentation also does not promise that a move of an arbitrary component set
is one database-level all-or-nothing transaction.

## 5. Nexus Repository 2 is different

Sonatype's [Upgrading Staging](https://help.sonatype.com/en/upgrading-staging.html)
distinguishes the generations. Nexus 2 created a dynamic staging Repository per
build and used the Maven-specific close/release suite. Nexus 3 uses one tag per
build, external CI workflow, and move/delete between a fixed set of Hosted
Repositories. The plugins are not compatible.

When asking for “Nexus-style staging,” a design must say whether it means the
historic Nexus 2/Maven Central close/release model or the current Nexus 3 Pro
tag-and-move model. The latter is the useful reference for this product.

## 6. Comparison with Artifact Gateway

| Dimension | Nexus ordinary Hosted | Gateway default direct | Gateway strict |
| --- | --- | --- | --- |
| Standard Maven/Gradle PUT | Saved and visible | Verified and visible | Verified and staged |
| Additional finalize | No | No | Gateway `coordinate:commit` |
| Unmodified client | Complete when client task succeeds | Same | PUT succeeds but no visibility without commit |
| Per-coordinate atomicity | No | No | Validated, atomically visible at commit |
| Mid-publish failure | Successful assets may remain visible | Same | Staged assets remain invisible |
| Integration cost | None | None | Gateway extension or CI wrapper |

The default Gateway path matches Nexus migration cost while retaining object
verification, immutable releases, authoritative checksum/metadata, and
PostgreSQL publication facts. Strict mode is intentionally stronger than
ordinary Nexus Hosted and therefore has an explicit integration cost.

## 7. Product implications

Artifact Gateway has implemented the evidence as a Repository policy:
`mavenStrictPublication` defaults to `false`; setting it to `true` enables
staging and `coordinate:commit`.

Two coherent higher-level models remain available:

1. Ordinary Hosted compatibility plus optional atomic mode: direct PUT for
   zero-change clients; strict mode for teams that accept a plugin or wrapper.
2. Isolated Hosted plus promotion: publish through standard Maven behavior to
   a development Hosted Repository, validate in CI, then promote the complete
   coordinate or build to the production Hosted Repository consumed by a
   production Group. This resembles Nexus 3 Pro Staging.

External documentation must explain both modes. Default mode enables zero-
change Nexus migration but does not promise multi-file coordinate atomicity.
Strict mode hides the coordinate until the Gateway-specific commit. Neither is
a cross-GAV reactor transaction.

## Primary references

- Sonatype: [Maven Repositories](https://help.sonatype.com/en/maven-repositories.html), [Deployment Policy](https://help.sonatype.com/en/configurable-repository-fields.html#deployment-policy), [Staging](https://help.sonatype.com/en/staging.html), [Staging Concepts](https://help.sonatype.com/en/staging-concepts.html), [Upgrading Staging](https://help.sonatype.com/en/upgrading-staging.html), [Nexus Repository Maven Plugin](https://help.sonatype.com/en/nexus-repository-maven-plugin.html)
- Apache Maven: [Maven Deploy Plugin](https://maven.apache.org/plugins/maven-deploy-plugin/) and [`deploy:deploy` parameters](https://maven.apache.org/plugins/maven-deploy-plugin/deploy-mojo.html)
- Gradle: [Maven Publish Plugin](https://docs.gradle.org/current/userguide/publishing_maven.html)
