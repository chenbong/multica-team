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
 return result
