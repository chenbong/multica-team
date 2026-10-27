"""Project-wide optional working directories; keep native sources untouched."""
def extend(upstream, files, entries):
 result=list(entries)
 def once(s,old,new):
  if s.count(old)!=1:raise SystemExit('project working-directory anchor changed: '+old[:100])
  return s.replace(old,new,1)
 def patch(rel,fn):
  nonlocal result
  source=next((s for n,s in result if n==rel),None)
  if source is None:source=((files/rel) if (files/rel).is_file() else (upstream/rel)).read_text()
  result=[(n,s) for n,s in result if n!=rel]+[(rel,fn(source))]
 patch('server/pkg/protocol/messages.go',lambda s:once(s,'const (\n\tDaemonCapabilitySkillBundlesV1','const (\n // DaemonCapabilityProjectWorkingDirectoryV1 resolves a project-wide path locally\n // and falls back only when it is missing.\n DaemonCapabilityProjectWorkingDirectoryV1 = "tmesh-project-working-directory-v1"\n\tDaemonCapabilitySkillBundlesV1'))
 patch('server/internal/daemon/client.go',lambda s:once(s,'\t\tprotocol.DaemonCapabilityLocalWorktreeV1,','\t\tprotocol.DaemonCapabilityLocalWorktreeV1,\n\t\tprotocol.DaemonCapabilityProjectWorkingDirectoryV1,'))
 def daemon(s):
  anchor='\tlocalRelease, abort := d.acquireLocalDirectoryLockIfNeeded(ctx, task, taskLog)'
  return once(s,anchor,''' resolvedTask, directoryErr := resolveProjectWorkingDirectory(task, d.cfg.DaemonID)
 if directoryErr != nil {
  taskLog.Error("project working directory resolution failed", "error", directoryErr)
  if err := d.reportTerminalTask(ctx, terminalTaskReport{kind:terminalTaskReportFail,taskID:task.ID,errorMessage:directoryErr.Error(),failureReason:"local_directory_error"}); err != nil { taskLog.Error("report project directory error", "error", err) }
  return
 }
 task = resolvedTask
'''+anchor)
 patch('server/internal/daemon/daemon.go',daemon)
 def resource(s):
  s=once(s,'\tcase "local_directory":\n\t\treturn validateLocalDirectoryRef(ref)','\tcase "local_directory":\n\t\treturn validateLocalDirectoryRef(ref)\n\tcase "working_directory":\n\t\treturn validateProjectWorkingDirectoryRef(ref)')
  anchor='\tif resourceType != "local_directory" {\n\t\treturn false, nil\n\t}'
  s=once(s,anchor,''' if resourceType == "working_directory" {
  rows,err:=h.Queries.ListProjectResources(ctx,projectID)
  if err!=nil{return false,err}
  for _,row:=range rows {if row.ResourceType==resourceType && (!excludeID.Valid || row.ID!=excludeID) {return true,nil}}
  return false,nil
 }
'''+anchor)
  s=s.replace('this daemon already has a local_directory attached to the project; remove it before adding another','This project already has a directory configured for this scope; edit or remove it before adding another.')
  return s
 patch('server/internal/handler/project_resource.go',resource)
 def project(s):
  s=once(s,'\tlocalDirSeen := map[string]int{}','\tlocalDirSeen := map[string]int{}\n projectDirectorySeen := false')
  anchor='\t\tnormalizedRefs[i] = ref'
  return once(s,anchor,'''  if res.ResourceType == "working_directory" {
   if projectDirectorySeen {writeError(w,http.StatusBadRequest,"only one project working directory is allowed");return}
   projectDirectorySeen=true
  }
'''+anchor)
 patch('server/internal/handler/project.go',project)
 return result
