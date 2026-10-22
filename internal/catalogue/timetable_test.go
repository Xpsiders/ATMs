package catalogue_test

import (
	"testing"

	"attendance-api/internal/catalogue"
)

func TestTimetableSlotsOverlap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		firstStart string
		firstEnd   string
		nextStart  string
		nextEnd    string
		want       bool
	}{
		{"partial overlap", "09:00", "10:30", "10:00", "11:00", true},
		{"contained", "09:00", "12:00", "10:00", "11:00", true},
		{"adjacent", "09:00", "10:00", "10:00", "11:00", false},
		{"separate", "09:00", "10:00", "11:00", "12:00", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := catalogue.TimetableSlotsOverlap(test.firstStart, test.firstEnd, test.nextStart, test.nextEnd)
			if err != nil || got != test.want {
				t.Fatalf("TimetableSlotsOverlap() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
