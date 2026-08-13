package api

import (
	"database/sql"
	"time"
)

// nullStr converts sql.NullString to *string for clean JSON serialization.
// null in JSON instead of {"String":"","Valid":false}
func nullStr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

// nullTime converts sql.NullTime to *time.Time for clean JSON serialization.
func nullTime(nt sql.NullTime) *time.Time {
	if nt.Valid {
		return &nt.Time
	}
	return nil
}
