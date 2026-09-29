package daemon

import (
	"errors"
	"time"
)

var errRegistrationCoolingDown = errors.New("runtime registration failed; waiting before retry")

type registrationFailure struct {
	fingerprint string
	attempts    int
	next        time.Time
}

func (d *Daemon) registrationRetryAllowed(workspace, fingerprint string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	state, ok := d.registrationFailures[workspace]
	return !ok || state.fingerprint != fingerprint || !now.Before(state.next)
}

func (d *Daemon) recordRegistrationResult(workspace, fingerprint string, usable bool, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if usable {
		delete(d.registrationFailures, workspace)
		return
	}
	if d.registrationFailures == nil {
		d.registrationFailures = make(map[string]registrationFailure)
	}
	state := d.registrationFailures[workspace]
	if state.fingerprint != fingerprint {
		state = registrationFailure{fingerprint: fingerprint}
	}
	if state.attempts < 4 {
		state.attempts++
	}
	delay := 15 * time.Second * time.Duration(1<<uint(state.attempts-1))
	state.next = now.Add(delay)
	d.registrationFailures[workspace] = state
}
