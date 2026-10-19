package app

import "github.com/ccsert/artifact-gateway/internal/uploadspool"

// Non-migrated format handlers share the same byte staging implementation.
// These local names contain no publication or format-specific behavior.
type uploadSpool = uploadspool.Spool

const uploadCopyBufferSize = uploadspool.CopyBufferSize

var errUploadTooLarge = uploadspool.ErrTooLarge
var spoolUpload = uploadspool.Read
var spoolObjectAppend = uploadspool.AppendObject
var uploadPartKey = uploadspool.PartKey
var spoolStoredUpload = uploadspool.ReadUpload
var deleteUploadObjects = uploadspool.DeleteUpload
