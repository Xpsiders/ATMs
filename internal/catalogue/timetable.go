package catalogue

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrTimetableOverlap = errors.New("timetable slot overlaps another slot at this venue")
	ErrSlotNotFound     = errors.New("timetable slot not found")
)

type TimetableSlot struct {
	ID         string `json:"id"`
	OfferingID string `json:"offering_id"`
	VenueID    string `json:"venue_id"`
	Weekday    int    `json:"weekday"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
}

type TimetableInput struct {
	VenueID  string `json:"venue_id" validate:"required,uuid"`
	Weekday  int    `json:"weekday" validate:"gte=0,lte=6"`
	StartsAt string `json:"starts_at" validate:"required"`
	EndsAt   string `json:"ends_at" validate:"required"`
}

type TimetableStore interface {
	ListTimetable(context.Context, string, string, string) ([]TimetableSlot, error)
	SaveTimetable(context.Context, string, string, string, string, TimetableInput) (TimetableSlot, error)
	DeleteTimetable(context.Context, string, string, string, string) error
}

func (s *Service) ListTimetable(ctx context.Context, institutionID, userID, offeringID string) ([]TimetableSlot, error) {
	store, ok := s.store.(TimetableStore)
	if !ok {
		return nil, errors.New("timetable operations are not supported by this store")
	}
	return store.ListTimetable(ctx, institutionID, userID, offeringID)
}

func (s *Service) SaveTimetable(ctx context.Context, institutionID, userID, offeringID, slotID string, input TimetableInput) (TimetableSlot, error) {
	if err := normalizeTimetableInput(&input); err != nil {
		return TimetableSlot{}, err
	}
	store, ok := s.store.(TimetableStore)
	if !ok {
		return TimetableSlot{}, errors.New("timetable operations are not supported by this store")
	}
	return store.SaveTimetable(ctx, institutionID, userID, offeringID, slotID, input)
}

func normalizeTimetableInput(input *TimetableInput) error {
	if input.Weekday < 0 || input.Weekday > 6 || input.VenueID == "" {
		return ErrInvalidRequest
	}
	start, startErr := time.Parse("15:04:05", input.StartsAt)
	end, endErr := time.Parse("15:04:05", input.EndsAt)
	if startErr != nil {
		start, startErr = time.Parse("15:04", input.StartsAt)
	}
	if endErr != nil {
		end, endErr = time.Parse("15:04", input.EndsAt)
	}
	if startErr != nil || endErr != nil || !end.After(start) {
		return ErrInvalidRequest
	}
	if len(input.StartsAt) == 5 {
		input.StartsAt += ":00"
	}
	if len(input.EndsAt) == 5 {
		input.EndsAt += ":00"
	}
	return nil
}

func (s *Service) DeleteTimetable(ctx context.Context, institutionID, userID, offeringID, slotID string) error {
	if slotID == "" {
		return fmt.Errorf("%w: slot ID is required", ErrInvalidRequest)
	}
	store, ok := s.store.(TimetableStore)
	if !ok {
		return errors.New("timetable operations are not supported by this store")
	}
	return store.DeleteTimetable(ctx, institutionID, userID, offeringID, slotID)
}

func TimetableSlotsOverlap(firstStart, firstEnd, secondStart, secondEnd string) (bool, error) {
	parse := func(value string) (time.Time, error) {
		if parsed, err := time.Parse("15:04:05", value); err == nil {
			return parsed, nil
		}
		return time.Parse("15:04", value)
	}
	a, err := parse(firstStart)
	if err != nil {
		return false, err
	}
	b, err := parse(firstEnd)
	if err != nil {
		return false, err
	}
	c, err := parse(secondStart)
	if err != nil {
		return false, err
	}
	d, err := parse(secondEnd)
	if err != nil {
		return false, err
	}
	if !b.After(a) || !d.After(c) {
		return false, ErrInvalidRequest
	}
	return a.Before(d) && c.Before(b), nil
}
