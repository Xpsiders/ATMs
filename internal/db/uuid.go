package db

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func NormalizeUUIDValue(value any) any {
	var bytes [16]byte
	switch value := value.(type) {
	case [16]byte:
		bytes = value
	case pgtype.UUID:
		if !value.Valid {
			return nil
		}
		bytes = value.Bytes
	default:
		return value
	}

	var encoded [32]byte
	hex.Encode(encoded[:], bytes[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" +
		string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" +
		string(encoded[20:32])
}

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
