package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func ParseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		if err == nil {
			err = fmt.Errorf("empty UUID")
		}
		return pgtype.UUID{}, fmt.Errorf("parse UUID: %w", err)
	}
	return id, nil
}

func TimestampPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}
