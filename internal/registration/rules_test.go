package registration

import (
	"errors"
	"testing"
)

func TestRegistrationModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mode       Mode
		registered bool
		target     bool
		wantStatus string
		wantError  bool
	}{
		{"auto target", ModeAuto, false, true, "approved", false},
		{"auto no target", ModeAuto, false, false, "pending", false},
		{"open", ModeOpen, false, false, "approved", false},
		{"approval unregistered", ModeApproval, false, false, "pending", false},
		{"approval registered", ModeApproval, true, false, "approved", false},
		{"closed registered", ModeClosed, true, false, "approved", false},
		{"closed unregistered", ModeClosed, false, false, "", true},
		{"invalid mode", Mode("other"), false, false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision, err := Evaluate(tc.mode, RosterRegisteredOnly, tc.registered, tc.target)
			if (err != nil) != tc.wantError {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if tc.wantError {
				if tc.mode == ModeClosed && !errors.Is(err, ErrRegistrationClosed) {
					t.Fatalf("closed registration error = %v", err)
				}
				return
			}
			wantOnRoster := tc.wantStatus == "approved"
			if decision.Status != tc.wantStatus || decision.OnRoster != wantOnRoster {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func TestOpenRosterPolicyFlagsUnregistered(t *testing.T) {
	t.Parallel()
	decision, err := Evaluate(ModeOpen, RosterOpen, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.OnRoster || decision.Status != "approved" {
		t.Fatalf("open roster decision = %+v", decision)
	}
	decision, err = Evaluate(ModeApproval, RosterOpen, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.OnRoster || decision.Status != "pending" {
		t.Fatalf("unregistered open-roster decision = %+v", decision)
	}
	if _, err := Evaluate(ModeOpen, RosterPolicy("other"), false, false); err == nil {
		t.Fatal("invalid roster policy accepted")
	}
}
