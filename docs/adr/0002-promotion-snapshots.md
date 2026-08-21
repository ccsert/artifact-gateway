# Promotion Uses Immutable Source Snapshots

[简体中文](0002-promotion-snapshots.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

Promotion records an immutable source snapshot and creates destination metadata
that reuses already verified content-addressed bytes; it never moves or
overwrites a source Artifact and does not copy bytes synchronously. This keeps
the worker retryable and idempotent, makes source/target authorization explicit,
and lets a later replication workflow own durable cross-store byte transfer
with checkpoints and integrity verification.
