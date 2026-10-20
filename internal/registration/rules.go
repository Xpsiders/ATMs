package registration

import "errors"

type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeOpen     Mode = "open"
	ModeApproval Mode = "approval"
	ModeClosed   Mode = "closed"
)

type RosterPolicy string

const (
	RosterRegisteredOnly RosterPolicy = "registered_only"
	RosterOpen           RosterPolicy = "open"
)

var ErrRegistrationClosed = errors.New("registration is closed")

type Decision struct {
	Allowed  bool
	Status   string
	OnRoster bool
}

func Evaluate(mode Mode, policy RosterPolicy, registered bool, autoTarget bool) (Decision, error) {
	decision := Decision{}
	switch mode {
	case ModeAuto:
		if registered || autoTarget {
			decision.Allowed, decision.Status = true, "approved"
		} else {
			decision.Allowed, decision.Status = true, "pending"
		}
	case ModeOpen:
		decision.Allowed, decision.Status = true, "approved"
	case ModeApproval:
		if registered {
			decision.Allowed, decision.Status = true, "approved"
		} else {
			decision.Allowed, decision.Status = true, "pending"
		}
	case ModeClosed:
		if !registered {
			return Decision{}, ErrRegistrationClosed
		}
		decision.Allowed, decision.Status = true, "approved"
	default:
		return Decision{}, errors.New("invalid registration mode")
	}

	approved := decision.Status == "approved"
	switch policy {
	case RosterRegisteredOnly:
		decision.OnRoster = approved
	case RosterOpen:
		decision.OnRoster = true
	default:
		return Decision{}, errors.New("invalid roster policy")
	}
	return decision, nil
}
