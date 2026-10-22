# APT first publication in Console

[简体中文](apt-first-publication.zh-CN.md) | [Documentation index](README.md)

The Console can publish the first signed snapshot of an empty suite in an APT
Hosted Repository. This is part of the existing operator preview. It does not
provide production key custody, install alerts, distribute trusted public keys,
or complete formal Hosted admission tracked by [#305](https://github.com/ccsert/artifact-gateway/issues/305).

An operator needs Repository administrator access. The Gateway must already have
the APT publication service and a trusted signer configured. See
[APT Hosted signing](apt-hosted-signing.md). A member without administrator access
does not fetch this page's administrator-only state.

## Stage, review, publish

1. Open the Repository's **APT snapshots** task. Enter the exact suite, such as
   `stable`, and choose **View**. The first-publication form appears only after
   the suite state loads and has no snapshot history or visible packages.
2. Select one non-empty `.deb` file, up to 1 GiB, and enter the component, such as
   `main`. Selection computes the file's exact size and SHA-256 in the browser.
   It sends no package or publication request. Use HTTPS or localhost so browser
   cryptography is available.
3. Choose **Stage and review package**. The generated management SDK creates an
   idempotent publication session and sends the original File as a raw Debian
   package body. The Gateway validates the bytes and parses the control metadata.
   Staging does not make a package available to APT clients.
4. Check the server-reported package name, version, architecture and canonical
   identity. Confirm **I checked the package name, version and architecture**.
   A revision whose Repository, digest or size differs from the selected file
   cannot enable publication.
5. Choose **Confirm and sign first snapshot**. This explicitly publishes
   `sequence: 1` containing only the staged session. On success, the page refreshes
   the current snapshot and shows its signing fingerprint and signer evidence.

The form is not an editor for existing snapshot membership. It never advances
the sequence or copies a subset of an existing suite into the full-publication
API. Existing snapshots keep the usual reviewed lifecycle, distribution and
archive operations. A concurrent first publication is subject to the server's
sequence and idempotency checks; this form does not overwrite it.

## Failures and retries

The selection, component, session and confirmed staged identity remain in memory
after validation, quota, signing or network failures. Once a request starts, its
file, component, body and idempotency key stay fixed. Retrying a lost create
response replays the same create request. Retrying upload uses the same session
and original File. Retrying publication uses the same key, session and sequence.
Double submission is blocked while a request is running.

A signer `503` keeps the staged package ready for retry. A lost publication
response is not treated as success: the same publication request must be retried.
After a confirmed success, a refresh error does not re-enable publication; use
**Refresh snapshots** to reload the confirmed current snapshot.

To change the file or component, explicitly choose **Clear this selection** and
confirm. Uploaded staging data expires on the server and is not published by
clearing the form. Draft files and retry keys are not saved in browser storage,
the URL or the shared query cache. Navigating to another Repository or suite, or
reloading the page, discards the local draft. Responses from the old target cannot
populate or publish the new target's form.

## Verification boundary

The Console regression tests use a fixed synthetic `.deb`, real browser
SHA-256 and the generated SDK's actual HTTP serializer. Simulated API responses
exercise staging, signer refusal, a lost committed response, exact retry and
desktop/narrow layouts in both languages. These browser fixtures do not prove a
real signer, PostgreSQL transaction, object store or APT installation. The
separate [APT Hosted distribution gate](apt-hosted-distribution.md) covers the
real backend and native client workflow. Neither evidence establishes production
key custody or formal admission.
