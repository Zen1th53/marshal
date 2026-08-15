package events

// DeliveryState models the explicit T43 producer-to-consumer lifecycle. Only
// the durable event row is authoritative after StateDurablyAppended; the
// in-process state value is a bounded operation result, not recovered state.
type DeliveryState string

const (
	StateProduced        DeliveryState = "produced"
	StateValidated       DeliveryState = "validated"
	StateDurablyAppended DeliveryState = "durably_appended"
	StatePublished       DeliveryState = "published"
	StateConsumed        DeliveryState = "consumed"
)

func (s DeliveryState) Valid() bool {
	switch s {
	case StateProduced, StateValidated, StateDurablyAppended, StatePublished, StateConsumed:
		return true
	default:
		return false
	}
}

func CanTransitionDelivery(from, to DeliveryState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	switch from {
	case StateProduced:
		return to == StateValidated
	case StateValidated:
		return to == StateDurablyAppended
	case StateDurablyAppended:
		return to == StatePublished
	case StatePublished:
		return to == StateConsumed
	default:
		return false
	}
}

// DeliveryResult reports how far one producer operation progressed. Event is
// populated once canonical persistence succeeds.
type DeliveryResult struct {
	State DeliveryState
	Event Event
}
