# Release Record Template

Create one copy in the approved release-tracking system for each production
deployment. Store command output as an attached CI artifact or restricted log
reference. Do not record bearer tokens, object-storage credentials, OIDC
tokens, or unredacted upstream URLs.

## Candidate

| Field | Value |
| --- | --- |
| Git revision | |
| Image digest or release tag | |
| Target environment | |
| Operator | |
| Reviewer | |
| UTC start | |
| UTC end | |
| Change or incident reference | |

## Automated Gates

| Gate | Result | UTC start/end | Output or CI artifact reference | Deviation |
| --- | --- | --- | --- | --- |
| make test | | | | |
| make integration-test | | | | |
| make native-oci-e2e | | | | |
| make native-raw-e2e | | | | |
| make native-maven-e2e | | | | |
| make native-npm-e2e | | | | |
| make native-pypi-e2e | | | | |
| make native-go-e2e | | | | |
| make conan-e2e | | | | |
| make readiness-e2e | | | | |
| make resolver-rotation-e2e | | | | |
| make oci-performance-e2e | | | | |
| make cache-operations-e2e | | | | |
| make openapi-check | | | | |
| make console-typecheck | | | | |
| make console-check | | | | |
| make console-test | | | | |
| make console-build | | | | |
| make console-e2e | | | | |
| make upgrade-readiness | | | | |
| make backup-restore-readiness | | | | |
| make native-apt-e2e | | | | |

## Production Verification

| Check | Evidence reference | Reviewer |
| --- | --- | --- |
| readyz returned 204 after deployment | | |
| Authenticated OCI and Maven reads succeeded | | |
| Raw GET and Conan 2 revision read succeeded when affected | | |
| Metrics reviewed for bounded authorization-denial signal | | |
| Audits reviewed for the rollout scope | | |
| Cache capacity and configured quotas reviewed | | |
| Upstream allowlists and Repository grants reviewed | | |
| OIDC issuer, audience, and HTTPS configuration reviewed | | |
| Cache collection status reviewed by an administrator | | |

## Approval And Rollback

| Field | Value |
| --- | --- |
| Approved by | |
| Approval UTC | |
| Previous image digest or tag | |
| Rollback owner | |
| Recovery runbook reference | docs/recovery-runbook.md |
| Deviations and accepted risk | |
