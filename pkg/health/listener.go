package health

// StateChange represents a change in health status of a target.
type StateChange struct {
	Address string
	Healthy bool
}

// Listener defines the interface for health state change notifications.
type Listener interface {
	OnStateChange(change StateChange)
}
