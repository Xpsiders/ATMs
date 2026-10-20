package db

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNormalizeUUIDValue(t *testing.T) {
	id := [16]byte{0x10, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0x10}
	validPgUUID := pgtype.UUID{Bytes: id, Valid: true}
	invalidPgUUID := pgtype.UUID{Bytes: id, Valid: false}

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{name: "byte array", input: id, want: "10000000-0000-4000-8000-000000000010"},
		{name: "valid pgtype UUID", input: validPgUUID, want: "10000000-0000-4000-8000-000000000010"},
		{name: "invalid pgtype UUID", input: invalidPgUUID, want: nil},
		{name: "other value", input: int64(42), want: int64(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeUUIDValue(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeUUIDValue(%#v) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeUUIDValueProducesStringInResponseMap(t *testing.T) {
	id := [16]byte{0x10, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0x10}
	body, err := json.Marshal(map[string]any{"id": NormalizeUUIDValue(id)})
	if err != nil {
		t.Fatalf("marshal response map: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal response map: %v", err)
	}
	if got, want := response["id"], "10000000-0000-4000-8000-000000000010"; got != want {
		t.Errorf("response id = %#v, want %#v", got, want)
	}
}
