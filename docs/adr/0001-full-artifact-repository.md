# Full Artifact Repository Is The Product Boundary

[简体中文](0001-full-artifact-repository.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

The historical compatibility clause below is superseded by
[ADR 0009](0009-native-architecture.md). It does not constrain current
management APIs, URL aliases, authentication models or internal interfaces.

Artifact Gateway will evolve from a primarily read-through gateway into a full
artifact repository for every supported format. Completeness means Hosted,
Proxy, and Group lifecycle; publication, immutable visibility, browsing/search,
logical deletion, retention, promotion, replication, authorization, audit, and
recovery. This does not imply adding every package ecosystem at once: a new
format is admitted only after its native protocol and full lifecycle contract
are defined. The current read-path compatibility remains stable while V3
write-model capabilities are introduced incrementally behind explicit contracts
and additive migrations.
