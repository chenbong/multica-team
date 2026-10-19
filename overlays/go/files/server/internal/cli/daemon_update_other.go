//go:build !linux

package cli

import "errors"

const DaemonUpdateHandoffEnv = "TEAM_MESH_UPDATE_HANDOFF"

func OpenDaemonUpdateLease() (*DaemonUpdateLease, *UpdateProbation, error) { return nil, nil, nil }
func (l *DaemonUpdateLease) Eligible() error {
	return errors.New("safe automatic updates currently support Linux only")
}
func (l *DaemonUpdateLease) Exclusive() error { return l.Eligible() }
func (l *DaemonUpdateLease) Shared() error    { return nil }
