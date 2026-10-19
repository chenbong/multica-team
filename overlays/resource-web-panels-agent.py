"""Checked server-owned skill wiring for the task-panel CLI contract."""


def generated(upstream, files):
    relative = "server/internal/service/builtin_skills.go"
    source = files / relative
    if not source.is_file():
        source = upstream / relative
    text = source.read_text()
    anchor = "return loadSkillDirs(builtinSkillsFS, builtinSkillsRoot, include)"
    if text.count(anchor) != 1:
        raise SystemExit("built-in skill loader changed; review task panel instruction wiring")
    skill = (upstream / "server/internal/service/builtin_skills/multica-platform/SKILL.md").read_text()
    for marker in ("issue and PR contracts,", "| `references/issues.md` | Issues:"):
        if skill.count(marker) != 1:
            raise SystemExit("platform skill routing changed; review task panel instruction wiring")
    return [(relative, text.replace(anchor, "return withResourceWebPanelSkill(loadSkillDirs(builtinSkillsFS, builtinSkillsRoot, include))", 1))]
