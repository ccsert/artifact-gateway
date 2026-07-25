package app

import mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"

// Compatibility aliases retain app runtime composition while Maven lifecycle
// behavior is owned by the Maven protocol module.
type NativeMavenMaintenance = mavenprotocol.NativeMaintenance
type NativeMavenRetention = mavenprotocol.NativeRetention
