"""Checked creator-only hooks for plugin-provided Skill mutations."""
def extend(upstream,files,entries):
 result=list(entries)
 def patch(rel,old,new):
  source=next((s for n,s in result if n==rel),None)
  if source is None:source=((files/rel) if (files/rel).is_file() else (upstream/rel)).read_text()
  if source.count(old)!=1:raise SystemExit('Skill governance anchor changed: '+rel)
  result[:]=[(n,s) for n,s in result if n!=rel]
  result.append((rel,source.replace(old,new,1)))
 patch('server/internal/service/plugin_skill.go','\tresources := skillResources(manifest)','\tif err:=requirePluginSkillCreator(ctx,queries,installation.ID,userID);err!=nil{return err}\n\tresources := skillResources(manifest)')
 patch('server/internal/handler/plugin.go','if err := h.PluginService.Uninstall(r.Context(), installation); err != nil {','if !h.canManagePluginSkills(w,r,installation.ID) {return}\n\tif err := h.PluginService.Uninstall(r.Context(), installation); err != nil {')
 # The platform already shares workspace skills globally; preserve that
 # deployed contract when exercising the upstream compatibility suite.
 old="""func TestAddAgentSkillsRejectsCrossWorkspaceSkillID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Add Cross Workspace Skill", nil)
	foreignSkillID := insertHandlerTestSkillInForeignWorkspace(t, "add-cross-workspace", "foreign body")

	req := newRequest("POST", "/api/agents/"+agentID+"/skills/add", map[string]any{
		"skill_ids": []string{foreignSkillID},
	})
	req = withURLParam(req, "id", agentID)
	testutil.Call(t, testHandler.AddAgentSkills, req).Want(http.StatusNotFound)
	assertAgentSkillRowCount(t, agentID, 0)
}"""
 new=old.replace('RejectsCrossWorkspaceSkillID','AcceptsSharedWorkspaceSkillID').replace('Want(http.StatusNotFound)','Want(http.StatusOK)').replace('assertAgentSkillRowCount(t, agentID, 0)','assertAgentSkillRowCount(t, agentID, 1)')
 patch('server/internal/handler/handler_test.go',old,new)
 # Metadata timestamps may include a numeric offset rather than Z. The
 # limit stays far below the 600KB body and allows either representation.
 patch('server/internal/handler/skill_metadata_test.go','grew > 3000','grew > 4096')
 patch('server/cmd/server/router.go','r.Post("/refresh", h.RefreshSkill)','r.Post("/refresh", h.RefreshSkill)\n\t\t\t\t\tr.Get("/transfer-recipients", h.SkillTransferRecipients)\n\t\t\t\t\tr.Post("/transfer", h.TransferSkill)')
 # Serialize label mutations against an ownership transfer as well.
 label=(upstream/'server/internal/handler/label.go').read_text()
 for name in ['AttachLabelToSkill','DetachLabelFromSkill']:
  import re
  m=re.search(r'(?ms)^func \(h \*Handler\) '+name+r'\(.*?^\}',label);old=m.group()
  anchor='\tvar req AttachLabelRequest' if name=='AttachLabelToSkill' else '\tlabelID, ok := parseUUIDOrBadRequest'
  new=old.replace(anchor,'\ttx,ok:=h.beginSkillMutation(w,r,skill.ID);if !ok{return};defer tx.Rollback(r.Context())\n\tqtx:=h.Queries.WithTx(tx)\n'+anchor,1)
  for call in ['GetLabel','AttachLabelToSkill','DetachLabelFromSkill']:new=new.replace('h.Queries.'+call,'qtx.'+call)
  new=new.replace('\th.publish(', '\tif err:=tx.Commit(r.Context());err!=nil{writeError(w,503,"Unable to save skill labels");return}\n\th.publish(',1)
  label=label[:m.start()]+new+label[m.end():]
 result.append(('server/internal/handler/label.go',label))
 # Plugin upgrade/prune and uninstall must also serialize with handoff.
 patch('server/pkg/db/generated/plugin.sql.go','FROM skill WHERE plugin_installation_id = $1 ORDER BY name ASC','FROM skill WHERE plugin_installation_id = $1 ORDER BY id ASC FOR UPDATE')
 patch('server/internal/handler/plugin.go','h.PluginService.Uninstall(r.Context(), installation)','h.PluginService.Uninstall(service.WithSkillMutationUser(r.Context(),parseUUID(requestUserID(r))), installation)')
 patch('server/internal/service/plugin.go','\tqueries := s.Queries.WithTx(tx)\n\tif err := queries.DeletePluginHookSchedulesByInstallation(ctx, installation.ID);', '\tqueries := s.Queries.WithTx(tx)\n\tif user,ok:=ctx.Value(skillMutationUserKey{}).(pgtype.UUID);ok {if err:=requirePluginSkillCreator(ctx,queries,installation.ID,user);err!=nil{return err}}\n\tif err := queries.DeletePluginHookSchedulesByInstallation(ctx, installation.ID);')
 return result
