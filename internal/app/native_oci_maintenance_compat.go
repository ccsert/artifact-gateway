package app

import ociprotocol "github.com/ccsert/artifact-gateway/internal/protocol/oci"

// NativeOCIMaintenance remains part of the app composition surface while its
// lifecycle behavior is owned by the OCI protocol module.
type NativeOCIMaintenance = ociprotocol.NativeMaintenance
type NativeOCIPromotion = ociprotocol.NativePromotion
