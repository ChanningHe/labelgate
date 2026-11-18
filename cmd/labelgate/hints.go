package main

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/store"
)

// containerUID is the user the published image runs as (Dockerfile USER nonroot).
const containerUID = "65532"

// storeHint returns "; <how to fix>" for a store open failure, or "" when
// there is no specific advice.
func storeHint(err error) string {
	dbPath := config.KeyWithEnv("db.path")
	switch {
	case errors.Is(err, store.ErrSchemaTooNew):
		return "; this database was written by a newer labelgate: run that version again, or point " + dbPath + " at another file"
	case errors.Is(err, store.ErrLegacyTooOld):
		return "; the file is not a labelgate database this release can upgrade: point " + dbPath + " at a new file"
	case isPermissionError(err):
		return "; labelgate cannot write the database: make its directory writable by the labelgate user (the image runs as UID " +
			containerUID + ": sudo chown -R " + containerUID + ":" + containerUID + " <host data dir>), or change " + dbPath
	}
	return ""
}

// isPermissionError recognises file permission failures, including SQLite's
// own wording, which does not wrap fs.ErrPermission.
func isPermissionError(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"permission denied", "readonly database", "unable to open database file"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
