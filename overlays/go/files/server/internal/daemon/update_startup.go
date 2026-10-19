package daemon

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func (d *Daemon) confirmUpdateStartup(ctx context.Context) error {
	probation := d.cfg.UpdateProbation
	if probation == nil {
		return nil
	}
	record, err := d.cfg.UpdateLease.Pending()
	if err != nil || record == nil || record.Target != strings.TrimPrefix(d.cfg.CLIVersion, "v") {
		return fmt.Errorf("update successor does not match the pending release")
	}
	if err := probation.Confirm(ctx, d.cfg.CLIVersion); err != nil {
		return err
	}
	if err := d.cfg.UpdateLease.Shared(); err != nil {
		return err
	}
	d.releaseClaimBarrier()
	close(probation.Done)
	return nil
}

func (d *Daemon) awaitUpdateProbation(ctx context.Context) bool {
	if d.cfg.UpdateProbation == nil {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-d.cfg.UpdateProbation.Done:
		return true
	}
}

func (d *Daemon) safeAutoUpdateSupported() bool {
	if runtime.GOOS != "linux" {
		d.logger.Info("auto-update: skipped (safe automatic upgrades currently support Linux only)")
		return false
	}
	if !cli.IsForkReleaseVersion(d.cfg.CLIVersion) {
		d.logger.Info("auto-update: skipped (not a tagged Team Mesh fork release)")
		return false
	}
	if err := d.cfg.UpdateLease.Eligible(); err != nil {
		d.logger.Info("auto-update: skipped", "reason", err)
		return false
	}
	return true
}

func (d *Daemon) deferGuardedUpdateResult(runtimeID, requestID string) bool {
	if d.cfg.UpdateLease == nil {
		return false
	}
	record, err := d.cfg.UpdateLease.Pending()
	if err != nil || record == nil {
		return false
	}
	d.restartMu.Lock()
	d.guardedUpdateRuntimeID, d.guardedUpdateRequestID = runtimeID, requestID
	d.restartMu.Unlock()
	return true
}

// FinishGuardedUpdate is called by the old guardian only after startup has
// either been committed or rolled back. A downloaded file is not success.
func (d *Daemon) FinishGuardedUpdate(startupErr error) {
	d.restartMu.Lock()
	runtimeID, requestID := d.guardedUpdateRuntimeID, d.guardedUpdateRequestID
	d.guardedUpdateRuntimeID, d.guardedUpdateRequestID = "", ""
	d.restartMu.Unlock()
	if runtimeID == "" || requestID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := map[string]any{"status": "completed", "output": "Daemon startup confirmed"}
	if startupErr != nil {
		payload = map[string]any{"status": "failed", "error": startupErr.Error()}
	}
	d.reportUpdateResult(ctx, runtimeID, requestID, payload)
}
