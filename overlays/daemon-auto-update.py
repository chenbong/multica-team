"""Checked Linux update handoff and polling hooks; never edit the submodule."""


def replace_once(text, old, new, name):
    if text.count(old) != 1:
        raise SystemExit(f"daemon upgrade anchor changed: {name}")
    return text.replace(old, new, 1)


def generated(upstream, files):
    rel = "server/internal/daemon/auto_update.go"
    text = (upstream / rel).read_text()
    text = replace_once(text, 'func (d *Daemon) autoUpdateLoop(ctx context.Context) {',
                        'func (d *Daemon) autoUpdateLoop(ctx context.Context) {\n'
                        '\tif !d.awaitUpdateProbation(ctx) { return }', "probation barrier")
    text = replace_once(text, '\tpullEnabled := d.cfg.AutoUpdateEnabled',
                        '\tpullEnabled := d.cfg.AutoUpdateEnabled\n'
                        '\tif pullEnabled { pullEnabled = d.safeAutoUpdateSupported() }', "Linux eligibility")
    text = replace_once(text, '\t\td.logger.Error("auto-update: upgrade completed but restart could not be scheduled — resuming claims")',
                        '\t\tif d.cfg.UpdateLease != nil {\n'
                        '\t\t\tif err := d.cfg.UpdateLease.Rollback(); err != nil {\n'
                        '\t\t\t\td.logger.Error("cannot recover staged upgrade", "error", err)\n'
                        '\t\t\t}\n'
                        '\t\t\t_ = d.cfg.UpdateLease.Shared()\n'
                        '\t\t}\n'
                        '\t\td.logger.Error("auto-update: upgrade completed but restart could not be scheduled — resuming claims")', "handoff resolution failure")
    yield rel, text

    rel = "server/cmd/multica/cmd_daemon.go"
    text = (upstream / rel).read_text()
    text = replace_once(text, '\td := daemon.New(cfg, logger)',
                        '\tif err := prepareDaemonUpdateStartup(&cfg, logger); err != nil { return err }\n'
                        '\tdefer cfg.UpdateLease.Close()\n'
                        '\td := daemon.New(cfg, logger)', "installation lease")
    text = replace_once(text, '\tdefer os.Remove(daemonPIDPathForProfile(profile))',
                        '\tdefer removeOwnedDaemonPID(daemonPIDPathForProfile(profile), os.Getpid())', "successor PID ownership")
    text = replace_once(text, '\t\targs := buildDaemonStartArgs(cmd)\n\t\tchild := exec.Command(restartBin, args...)',
                        '\t\targs := buildDaemonStartArgs(cmd)\n'
                        '\t\tif handled, err := guardedDaemonHandoff(restartBin, args, cfg, daemonPIDPathForProfile(profile), daemonStderrLogPathForProfile(profile), logger); handled { d.FinishGuardedUpdate(err); return err }\n'
                        '\t\tchild := exec.Command(restartBin, args...)', "guarded successor")
    yield rel, text
