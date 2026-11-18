package main

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/store"
)

func TestStoreHint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"permission", &fs.PathError{Op: "mkdir", Path: "/app/config", Err: fs.ErrPermission},
			[]string{"UID 65532", "chown", "db.path (env LABELGATE_DB_PATH)"}},
		{"sqlite cannot open", errors.New("store: connect x: unable to open database file: out of memory (14)"),
			[]string{"writable", "db.path (env LABELGATE_DB_PATH)"}},
		{"schema too new", fmt.Errorf("%w: database is at version 9", store.ErrSchemaTooNew),
			[]string{"newer labelgate", "db.path (env LABELGATE_DB_PATH)"}},
		{"legacy too old", fmt.Errorf("%w: table x", store.ErrLegacyTooOld),
			[]string{"not a labelgate database", "db.path"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storeHint(tt.err)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("storeHint = %q, want %q", got, w)
				}
			}
		})
	}
	if got := storeHint(errors.New("disk I/O error")); got != "" {
		t.Errorf("unknown error got hint %q", got)
	}
}
