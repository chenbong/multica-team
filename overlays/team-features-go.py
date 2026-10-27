"""Compose checked application hooks without modifying the upstream checkout."""

def extend(upstream, files, entries):
 result=list(entries)
 def patch(relative, rewrite):
  nonlocal result
  text=next((s for n,s in result if n==relative),None)
  if text is None:
   source=files/relative
   text=(source if source.is_file() else upstream/relative).read_text()
  result=[(n,s) for n,s in result if n!=relative]
  result.append((relative,rewrite(text)))
 def once(s,old,new):
  if s.count(old)!=1:raise SystemExit('six-feature anchor changed: '+old[:100])
  return s.replace(old,new,1)
 def issue_queries(s):
  import re
  for name in ('CreateIssue','CreateIssueWithOrigin'):
   pattern=rf'(?ms)^func \(q \*Queries\) {name}\(.*?^\}}'
   match=re.search(pattern,s)
   if not match:raise SystemExit('issue creation hook missing: '+name)
   old=match.group()
   new=once(old,'\treturn i, err','\tif err == nil { _, err = q.db.Exec(ctx, "SELECT team_mesh_ensure_issue_managers($1)", i.ID) }\n\treturn i, err')
   s=s[:match.start()]+new+s[match.end():]
  return s
 patch('server/pkg/db/generated/issue.sql.go',issue_queries)
 def delete_queries(s):
  anchor='WITH target AS (\n    SELECT issue.id FROM issue WHERE issue.id = $1 AND issue.workspace_id = $2'
  s=once(s,anchor,'WITH deleted_issue_manager_state AS (DELETE FROM issue_manager_state WHERE issue_id=$1 AND EXISTS(SELECT 1 FROM issue WHERE id=$1 AND workspace_id=$2)), deleted_issue_managers AS (DELETE FROM issue_manager WHERE issue_id=$1 AND EXISTS(SELECT 1 FROM issue WHERE id=$1 AND workspace_id=$2)), target AS (\n    SELECT issue.id FROM issue WHERE issue.id = $1 AND issue.workspace_id = $2')
  return s
 patch('server/pkg/db/generated/issue.sql.go',delete_queries)
 def delete_workspace(s):
  anchor='const deleteWorkspaceIssueRoots = `-- name: DeleteWorkspaceIssueRoots :exec\nWITH '
  return once(s,anchor,anchor+'deleted_owner_read_scopes AS (DELETE FROM agent_token_read_scope WHERE workspace_id=$1), deleted_issue_manager_state AS (DELETE FROM issue_manager_state WHERE issue_id IN (SELECT id FROM issue WHERE workspace_id=$1)), deleted_issue_managers AS (DELETE FROM issue_manager WHERE issue_id IN (SELECT id FROM issue WHERE workspace_id=$1)), ')
 patch('server/pkg/db/generated/workspace_delete.sql.go',delete_workspace)

 def issue_handlers(s):
  import re
  for name in ('DeleteIssue','BatchDeleteIssues'):
   match=re.search(rf'(?ms)^func \(h \*Handler\) {name}\(.*?^\}}',s)
   if not match:raise SystemExit('delete hook missing: '+name)
   old=match.group()
   if name=='DeleteIssue':
    new=once(old,'\th.TaskService.CancelTasksForIssue(r.Context(), issue.ID)','\tif !h.requireIssueDeletionManager(w,r,issue) { return }\n\tr=r.WithContext(withIssueDeleteActor(r.Context(),requestUserID(r)))\n\tif !h.cancelIssueForHumanDeletion(w,r,issue) { return }')
    new=once(new,'\t_ = h.AutopilotService.FailAutopilotRunsByIssue(r.Context(), issue.ID)\n','')
   else:
    new=once(old,'\t\tseenIssueIDs[issueUUID] = struct{}{}','\t\tif !h.requireIssueDeletionManager(w,r,issue) { return }\n\t\tseenIssueIDs[issueUUID] = struct{}{}')
    new=once(new,'\t\th.TaskService.CancelTasksForIssue(r.Context(), issue.ID)\n\t\t_ = h.AutopilotService.FailAutopilotRunsByIssue(r.Context(), issue.ID)\n','')
    new=once(new,'\tdeleteResult, err := h.deleteIssuesAndCollectAttachmentURLs(', '\tr=r.WithContext(withIssueDeleteActor(r.Context(),userID))\n\tfor _,issue:=range issues { if !h.cancelIssueForHumanDeletion(w,r,issue) {return} }\n\tdeleteResult, err := h.deleteIssuesAndCollectAttachmentURLs(')
   s=s[:match.start()]+new+s[match.end():]
  s=once(s,'\t\tdetached, err := qtx.DetachDirectChildIssues(', '\t\tif err:=validateIssueDeleteActor(ctx,tx,issue); err!=nil {return issueDeleteResult{},err}\n\t\tif _,err:=tx.Exec(ctx,`DELETE FROM issue_manager WHERE issue_id=$1`,issue.ID);err!=nil{return issueDeleteResult{},err}\n\t\tdetached, err := qtx.DetachDirectChildIssues(')
  return s
 patch('server/internal/handler/issue.go',issue_handlers)
 def workspace_revoke(s):
  anchor='\tif err := tx.Commit(ctx); err != nil {'
  return once(s,anchor,'\tif err:=repairIssueManagersAfterDeparture(ctx,tx,workspaceID,userID);err!=nil{return empty,err}\n'+anchor)
 patch('server/internal/handler/workspace_revoke.go',workspace_revoke)
 def file_upload(s):
  s=once(s,'const maxUploadSize = 100 << 20 // 100 MB','const maxUploadSize = 50 << 20 // 50 MiB')
  s=once(s,'MarkdownURL string `json:"markdown_url"`','LegacyURLs []string `json:"legacy_urls,omitempty"`\n\tMarkdownURL string `json:"markdown_url"`')
  s=once(s,'\t// Only CloudFront mode overrides the stable path here;', '\tresp.LegacyURLs = h.legacyAttachmentURLs(a)\n\t// Only CloudFront mode overrides the stable path here;')
  s=once(s,'strings.TrimRight(h.cfg.PublicURL, "/")','h.attachmentPublicURL()')
  s=once(s,'http.MaxBytesReader(w, r.Body, maxUploadSize)','http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))')
  s=once(s,'r.ParseMultipartForm(maxUploadSize)','r.ParseMultipartForm(1 << 20)')
  s=once(s,'writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")','var limitErr *http.MaxBytesError; if errors.As(err, &limitErr) { writeError(w, http.StatusRequestEntityTooLarge, "Upload exceeds 50 MiB") } else { writeError(w,http.StatusBadRequest,"Invalid multipart form") }')
  s=once(s,'\tdefer file.Close()','\tdefer file.Close()\n\tif header.Size > maxUploadSize { writeError(w,413,"Attachment exceeds 50 MiB");return }\n\tif len(r.MultipartForm.File["file"]) != 1 {writeError(w,400,"Upload one file per request");return}')
  s=once(s,'\tdata, err := io.ReadAll(file)\n\tif err != nil {\n\t\twriteError(w, http.StatusBadRequest, "failed to read file")\n\t\treturn\n\t}\n','')
  s=once(s,'SizeBytes:    int64(len(data)),','SizeBytes:    header.Size,')
  s=once(s,'\t\tlink, err := h.Storage.Upload(r.Context(), key, data, contentType, header.Filename)','\t\tlink, err := uploadAttachmentReader(r.Context(), h.Storage, key, file, header.Size, contentType, header.Filename)')
  s=once(s,'\tlink, err := h.Storage.Upload(r.Context(), key, data, contentType, header.Filename)','\tlink, err := uploadAttachmentReader(r.Context(), h.Storage, key, file, header.Size, contentType, header.Filename)')
  old='\t\t\t// S3 upload succeeded but DB record failed — still return the link\n\t\t\t// so the file is usable. Log the error for investigation.'
  s=once(s,old,'\t\t\th.deleteS3Objects(r.Context(),[]string{link})\n\t\t\twriteError(w,503,"Unable to save attachment metadata; retry the upload")\n\t\t\treturn')
  s=once(s,'\t\twriteJSON(w, http.StatusOK, map[string]string{\n\t\t\t"id":       "",\n\t\t\t"url":      link,\n\t\t\t"filename": header.Filename,\n\t\t})\n\t\treturn\n','')
  s=once(s,'\tlocal, ok := h.Storage.(*storage.LocalStorage)\n\tif !ok {','\tlocal := storage.LegacyLocalStorage(h.Storage)\n\tif local == nil {')
  s=once(s,'\tkey := strings.TrimPrefix(r.URL.Path, "/uploads/")','\tkey := strings.TrimPrefix(r.URL.Path, "/uploads/")\n\tif h.serveMigratedAttachment(w,r,key) {return}')
  anchor='\tuserID, ok := requireUserID(w, r)'
  import re
  match=re.search(r'(?ms)^func \(h \*Handler\) loadAttachmentForDownload\(.*?^\}',s)
  if not match:raise SystemExit('attachment download scope hook missing')
  old=match.group();new=once(old,anchor,'\tif r.Header.Get("X-Actor-Source")=="task_token" && r.Header.Get("X-Workspace-ID")!=uuidToString(att.WorkspaceID) {writeError(w,404,"attachment not found");return db.Attachment{},false}\n'+anchor)
  s=s[:match.start()]+new+s[match.end():]
  s=once(s,'func (h *Handler) resolveAttachmentDownloadMode(rawURL string) attachmentDownloadMode {','func (h *Handler) resolveAttachmentDownloadMode(rawURL string) attachmentDownloadMode {\n\tif storage.IsLegacyAttachment(h.Storage,rawURL) {return attachmentDownloadModeProxy}')
  return s
 patch('server/internal/handler/file.go',file_upload)
 def attachment_queries(s):
  anchor='WITH inserted AS (\n  INSERT INTO attachment ('
  s=once(s,anchor,'WITH target_issue AS MATERIALIZED(SELECT id FROM issue WHERE id=$9 AND workspace_id=$2 FOR KEY SHARE), inserted AS (\n  INSERT INTO attachment (')
  old='  VALUES (\n    $1, $2, $9, $10, $11, $12, $13,\n    $3, $4, $5, $6, $7, $8\n  )'
  new='  SELECT $1, $2, $9, $10, $11, $12, $13, $3, $4, $5, $6, $7, $8 WHERE $9::uuid IS NULL OR EXISTS(SELECT 1 FROM target_issue)'
  return once(s,old,new)
 patch('server/pkg/db/generated/attachment.sql.go',attachment_queries)
 def avatar_namespace(s):
  return once(s,'strings.HasPrefix(key, workspaceUploadNamespace)','strings.HasPrefix(storage.AttachmentLogicalKey(h.Storage,key), workspaceUploadNamespace)')
 patch('server/internal/handler/avatar.go',avatar_namespace)
 def source_cleanup(s):
  s=once(s,'s.SourceContextStorage.DeleteObject(deleteCtx, intent.StorageKey)','s.SourceContextStorage.DeleteObject(deleteCtx, storage.AttachmentDeletionKey(s.SourceContextStorage,intent.StorageKey,intent.ObjectUrl))')
  if '"github.com/multica-ai/multica/server/internal/storage"' not in s:s=once(s,'import (','import (\n "github.com/multica-ai/multica/server/internal/storage"')
  return s
 patch('server/internal/service/source_context.go',source_cleanup)
 def channel_cleanup(s):
  s=once(s,'r.Storage.DeleteObject(delCtx, row.StorageKey)','r.Storage.DeleteObject(delCtx, storage.AttachmentDeletionKey(r.Storage,row.StorageKey,row.StorageUrl))')
  if '"github.com/multica-ai/multica/server/internal/storage"' not in s:s=once(s,'import (','import (\n "github.com/multica-ai/multica/server/internal/storage"')
  return s
 patch('server/internal/service/channel_media_reconciler.go',channel_cleanup)
 def chat_uploader(s):
  return once(s,'req.Content, attachmentIDs, actorType, parseUUID(actorID))','req.Content, attachmentIDs, actorType, infoFlowChatAttachmentUploader(r,session,parseUUID(actorID)))')
 patch('server/internal/handler/chat.go',chat_uploader)
 def token_cleanup(s):
  s=once(s,'DELETE FROM task_token WHERE task_id = $1','WITH removed_owner_scope AS (DELETE FROM agent_token_read_scope WHERE task_id=$1) DELETE FROM task_token WHERE task_id = $1')
  s=once(s,'DELETE FROM task_token WHERE expires_at <= now()','WITH removed_owner_scope AS (DELETE FROM agent_token_read_scope s WHERE NOT EXISTS(SELECT 1 FROM task_token t WHERE t.token_hash=s.token_hash AND t.expires_at>now())) DELETE FROM task_token WHERE expires_at <= now()')
  return s
 patch('server/pkg/db/generated/task_token.sql.go',token_cleanup)
 return result
