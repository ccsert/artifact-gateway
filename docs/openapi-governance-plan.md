# OpenAPI Governance Plan

Status: approved next-step plan. This document deliberately does not start the
OpenAPI file split; the current priority is preserving a verified contract for
the protocols already implemented.

## Target source layout

The manually maintained source will move from the single bundled JSON document
to a multi-file YAML source tree:

```text
api/openapi/native-hosted.yaml
api/openapi/components/*.yaml
api/openapi/management/*.yaml
api/openapi/protocols/*.yaml
```

`api/openapi/native-hosted-v1.json` will then become a generated bundle. It
must not be hand-edited after the source split. The bundle remains versioned so
the Console client, API-diff check, release artifacts, and external consumers
can use one resolved document.

## Build and review gates

The follow-up implementation will add these Make targets:

- `make openapi-bundle`: resolve the YAML entrypoint and references into the
  versioned JSON bundle.
- `make openapi-check`: regenerate the bundle and fail on a diff, validate the
  resolved OpenAPI document, run contract tests, and regenerate/check the
  Console client when it consumes this contract.

Every management route change must update its source fragment, generated
bundle, contract test, and generated client in the same reviewable change.
The generator version and bundling configuration must be pinned in the
repository. CI must run `make openapi-check` before any generated contract is
accepted.

## Code generation boundary

Management APIs are conventional resource routes and are the first candidate
for `oapi-codegen`: generate request/response types and a strict server
interface, while keeping authorization and transaction orchestration in the
handwritten implementation.

Protocol APIs must not be blindly code-generated. OCI Registry V2, Raw, Maven,
and Conan HTTP behavior is defined first by their official specifications and
ecosystem client expectations. For each protocol, record an official-spec
reference plus a Gateway compatibility overlay; verify that overlay through
focused handler/contract tests and executable E2E fixtures. OpenAPI documents
the exposed Gateway surface but does not replace protocol conformance work.

## Migration sequence

1. Keep the present JSON bundle stable while contract and protocol E2E gates
   remain green.
2. Introduce a pinned bundler and reproduce the current bundle byte-for-byte
   except for intentional formatting normalization.
3. Move management paths and shared schemas into YAML fragments first, then
   protocol overlays one protocol at a time.
4. Add `make openapi-bundle` and `make openapi-check`, then make CI enforce
   them.
5. Evaluate `oapi-codegen` for management APIs in a separately reviewable
   change; do not couple it to protocol handler extraction.
