package postgres

import (
	"context"
	"errors"
	"fmt"

	"attendance-api/internal/catalogue"
	"attendance-api/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ListTimetable(ctx context.Context, institutionIDText, userIDText, offeringIDText string) ([]catalogue.TimetableSlot, error) {
	tenant, user, offering, err := timetableIDs(institutionIDText, userIDText, offeringIDText)
	if err != nil {
		return nil, err
	}
	allowed, err := canManageTimetable(ctx, s.pool, tenant, user, offering)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, catalogue.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text, offering_id::text, venue_id::text,
		weekday, starts_at::text, ends_at::text
		FROM timetable WHERE institution_id=$1 AND offering_id=$2 ORDER BY weekday, starts_at, id`,
		tenant, offering)
	if err != nil {
		return nil, fmt.Errorf("list offering timetable: %w", err)
	}
	defer rows.Close()
	items := make([]catalogue.TimetableSlot, 0)
	for rows.Next() {
		var item catalogue.TimetableSlot
		if err := rows.Scan(&item.ID, &item.OfferingID, &item.VenueID, &item.Weekday, &item.StartsAt, &item.EndsAt); err != nil {
			return nil, fmt.Errorf("scan timetable slot: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timetable slots: %w", err)
	}
	return items, nil
}

func (s *Store) SaveTimetable(ctx context.Context, institutionIDText, userIDText, offeringIDText, slotIDText string, input catalogue.TimetableInput) (catalogue.TimetableSlot, error) {
	tenant, user, offering, err := timetableIDs(institutionIDText, userIDText, offeringIDText)
	if err != nil {
		return catalogue.TimetableSlot{}, err
	}
	venue, err := db.ParseUUID(input.VenueID)
	if err != nil {
		return catalogue.TimetableSlot{}, catalogue.ErrInvalidRequest
	}
	slotID := pgtype.UUID{}
	action := "timetable.created"
	if slotIDText != "" {
		slotID, err = db.ParseUUID(slotIDText)
		if err != nil {
			return catalogue.TimetableSlot{}, catalogue.ErrSlotNotFound
		}
		action = "timetable.updated"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("begin timetable update: %w", err)
	}
	defer tx.Rollback(ctx)
	allowed, err := canManageTimetable(ctx, tx, tenant, user, offering)
	if err != nil {
		return catalogue.TimetableSlot{}, err
	}
	if !allowed {
		return catalogue.TimetableSlot{}, catalogue.ErrForbidden
	}
	var venueActive bool
	if err := tx.QueryRow(ctx, `SELECT active FROM venues WHERE institution_id=$1 AND id=$2`,
		tenant, venue).Scan(&venueActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return catalogue.TimetableSlot{}, catalogue.ErrNotFound
		}
		return catalogue.TimetableSlot{}, fmt.Errorf("check timetable venue: %w", err)
	}
	if !venueActive {
		return catalogue.TimetableSlot{}, catalogue.ErrInvalidRequest
	}
	if slotID.Valid {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM timetable WHERE institution_id=$1 AND offering_id=$2 AND id=$3)",
			tenant, offering, slotID).Scan(&exists); err != nil {
			return catalogue.TimetableSlot{}, fmt.Errorf("check timetable slot: %w", err)
		}
		if !exists {
			return catalogue.TimetableSlot{}, catalogue.ErrSlotNotFound
		}
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		fmt.Sprintf("%s:%s:%d", tenant.String(), venue.String(), input.Weekday)); err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("lock venue timetable: %w", err)
	}
	var overlaps bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM timetable t
		WHERE t.institution_id=$1 AND t.venue_id=$2 AND t.weekday=$3
		  AND t.starts_at < $5::time AND $4::time < t.ends_at
		  AND ($6::uuid IS NULL OR t.id <> $6)
	)`, tenant, venue, input.Weekday, input.StartsAt, input.EndsAt, slotID).Scan(&overlaps); err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("check timetable overlap: %w", err)
	}
	if overlaps {
		return catalogue.TimetableSlot{}, catalogue.ErrTimetableOverlap
	}
	var item catalogue.TimetableSlot
	if slotID.Valid {
		err = tx.QueryRow(ctx, `UPDATE timetable SET venue_id=$4, weekday=$5, starts_at=$6, ends_at=$7
			WHERE institution_id=$1 AND offering_id=$2 AND id=$3
			RETURNING id::text, offering_id::text, venue_id::text, weekday, starts_at::text, ends_at::text`,
			tenant, offering, slotID, venue, input.Weekday, input.StartsAt, input.EndsAt).Scan(
			&item.ID, &item.OfferingID, &item.VenueID, &item.Weekday, &item.StartsAt, &item.EndsAt)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO timetable(institution_id, offering_id, venue_id, weekday, starts_at, ends_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			RETURNING id::text, offering_id::text, venue_id::text, weekday, starts_at::text, ends_at::text`,
			tenant, offering, venue, input.Weekday, input.StartsAt, input.EndsAt).Scan(
			&item.ID, &item.OfferingID, &item.VenueID, &item.Weekday, &item.StartsAt, &item.EndsAt)
	}
	if err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("save timetable slot: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,$3,'timetable',$4,jsonb_build_object('offering_id',$5::text,'venue_id',$6::text,'weekday',$7::int))`,
		tenant, user, action, item.ID, item.OfferingID, item.VenueID, item.Weekday); err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("audit timetable change: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogue.TimetableSlot{}, fmt.Errorf("commit timetable change: %w", err)
	}
	return item, nil
}

func (s *Store) DeleteTimetable(ctx context.Context, institutionIDText, userIDText, offeringIDText, slotIDText string) error {
	tenant, user, offering, err := timetableIDs(institutionIDText, userIDText, offeringIDText)
	if err != nil {
		return err
	}
	slotID, err := db.ParseUUID(slotIDText)
	if err != nil {
		return catalogue.ErrSlotNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin timetable deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	allowed, err := canManageTimetable(ctx, tx, tenant, user, offering)
	if err != nil {
		return err
	}
	if !allowed {
		return catalogue.ErrForbidden
	}
	tag, err := tx.Exec(ctx, "DELETE FROM timetable WHERE institution_id=$1 AND offering_id=$2 AND id=$3",
		tenant, offering, slotID)
	if err != nil {
		return fmt.Errorf("delete timetable slot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return catalogue.ErrSlotNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,'timetable.deleted','timetable',$3,jsonb_build_object('offering_id',$4::uuid))`,
		tenant, user, slotID, offering); err != nil {
		return fmt.Errorf("audit timetable deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit timetable deletion: %w", err)
	}
	return nil
}

type timetableQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func canManageTimetable(ctx context.Context, q timetableQuerier, tenant, user, offering pgtype.UUID) (bool, error) {
	var allowed bool
	err := q.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM course_offerings o
		JOIN courses c ON c.institution_id=o.institution_id AND c.id=o.course_id
		JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
		WHERE o.institution_id=$1 AND o.id=$3 AND (
			o.coordinator_user_id=$2 OR EXISTS (
				SELECT 1 FROM offering_lecturers ol WHERE ol.institution_id=o.institution_id
				  AND ol.offering_id=o.id AND ol.lecturer_user_id=$2)
			OR EXISTS (SELECT 1 FROM role_assignments ra WHERE ra.institution_id=o.institution_id
				AND ra.user_id=$2 AND ((ra.role='department_admin' AND ra.scope_type='department' AND ra.department_id=d.id)
				OR (ra.role='faculty_admin' AND ra.scope_type='faculty' AND ra.faculty_id=d.faculty_id)
				OR (ra.role='institution_admin' AND ra.scope_type='institution') OR ra.role='platform_admin'))
		)
	)`, tenant, user, offering).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("authorize timetable access: %w", err)
	}
	return allowed, nil
}

func timetableIDs(institutionIDText, userIDText, offeringIDText string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	user, err := db.ParseUUID(userIDText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	offering, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, catalogue.ErrNotFound
	}
	return tenant, user, offering, nil
}

var _ catalogue.TimetableStore = (*Store)(nil)
