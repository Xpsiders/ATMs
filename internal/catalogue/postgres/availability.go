package postgres

import (
	"context"
	"fmt"

	"attendance-api/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) addOfferingAvailability(ctx context.Context, tenant pgtype.UUID, offerings []map[string]any) error {
	if len(offerings) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(offerings))
	for _, offering := range offerings {
		text, ok := offering["id"].(string)
		if !ok {
			return fmt.Errorf("offering result is missing a string ID")
		}
		id, err := db.ParseUUID(text)
		if err != nil {
			return fmt.Errorf("parse offering result ID: %w", err)
		}
		ids = append(ids, id)
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id::text,
		EXISTS(SELECT 1 FROM lecture_sessions ls WHERE ls.institution_id=o.institution_id
			AND ls.offering_id=o.id AND ls.ended_at IS NULL),
		slot.id::text, slot.weekday, slot.starts_at::text, slot.ends_at::text,
		slot.venue_id::text, venue.name, slot.next_at
		FROM course_offerings o
		LEFT JOIN LATERAL (
			SELECT t.id, t.weekday, t.starts_at, t.ends_at, t.venue_id,
				(((now() AT TIME ZONE 'Africa/Lagos')::date
				  + ((t.weekday - extract(dow FROM (now() AT TIME ZONE 'Africa/Lagos'))::integer + 7) % 7
				  + CASE WHEN t.weekday = extract(dow FROM (now() AT TIME ZONE 'Africa/Lagos'))::integer
					AND t.starts_at <= (now() AT TIME ZONE 'Africa/Lagos')::time THEN 7 ELSE 0 END)
				 + t.starts_at) AT TIME ZONE 'Africa/Lagos') AS next_at
			FROM timetable t
			WHERE t.institution_id=o.institution_id AND t.offering_id=o.id
			ORDER BY ((t.weekday - extract(dow FROM (now() AT TIME ZONE 'Africa/Lagos'))::integer + 7) % 7
				+ CASE WHEN t.weekday = extract(dow FROM (now() AT TIME ZONE 'Africa/Lagos'))::integer
					AND t.starts_at <= (now() AT TIME ZONE 'Africa/Lagos')::time THEN 7 ELSE 0 END),
				t.starts_at
			LIMIT 1
		) slot ON TRUE
		LEFT JOIN venues venue ON venue.institution_id=o.institution_id AND venue.id=slot.venue_id
		WHERE o.institution_id=$1 AND o.id=ANY($2::uuid[])`, tenant, ids)
	if err != nil {
		return fmt.Errorf("load offering timetable availability: %w", err)
	}
	defer rows.Close()
	type availability struct {
		open bool
		slot map[string]any
	}
	byID := make(map[string]availability, len(offerings))
	for rows.Next() {
		var id string
		var open bool
		var slotID, startsAt, endsAt, venueID, venueName pgtype.Text
		var weekday pgtype.Int2
		var nextAt pgtype.Timestamptz
		if err := rows.Scan(&id, &open, &slotID, &weekday, &startsAt, &endsAt, &venueID, &venueName, &nextAt); err != nil {
			return fmt.Errorf("scan offering timetable availability: %w", err)
		}
		var nextSlot map[string]any
		if slotID.Valid {
			nextSlot = map[string]any{
				"id": slotID.String, "weekday": int(weekday.Int16),
				"starts_at": startsAt.String, "ends_at": endsAt.String,
				"venue_id": venueID.String, "venue_name": venueName.String,
			}
			if nextAt.Valid {
				nextSlot["next_at"] = nextAt.Time.UTC()
			}
		}
		byID[id] = availability{open: open, slot: nextSlot}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate offering timetable availability: %w", err)
	}
	for _, offering := range offerings {
		id := offering["id"].(string)
		value, ok := byID[id]
		if !ok {
			return fmt.Errorf("availability missing for offering %s", id)
		}
		offering["session_open"] = value.open
		offering["next_timetable_slot"] = value.slot
	}
	return nil
}
