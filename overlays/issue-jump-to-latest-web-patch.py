"""Add an issue-scoped jump without changing default reading positions."""


def extend(changes):
    path = "packages/views/issues/components/issue-detail.tsx"
    changes.setdefault(path, []).extend([
        ('import { ThreadMinimap, type ThreadMinimapThread } from "./thread-minimap";\n',
         'import { ThreadMinimap, type ThreadMinimapThread } from "./thread-minimap";\n'
         'import { IssueJumpToLatest } from "./issue-jump-to-latest";\n'),
        ('        {/* Thread quick-jump rail — rides the scroll container\'s right edge,\n',
         '        {!isPeek && (\n'
         '          <IssueJumpToLatest\n'
         '            key={`${wsId}:${id}`}\n'
         '            scrollContainerEl={scrollContainerEl}\n'
         '            composerRef={composerRef}\n'
         '            stickyComposer={stickyComposer}\n'
         '            onPrepareJump={() => {\n'
         '              if (items.length && !highlightCommentId && !find.open) {\n'
         '                virtuosoRef.current?.scrollToIndex({ index: items.length - 1, align: "end", behavior: "auto" });\n'
         '              }\n'
         '            }}\n'
         '          />\n'
         '        )}\n\n'
         '        {/* Thread quick-jump rail — rides the scroll container\'s right edge,\n'),
    ])
    labels = {"en": "Latest replies", "zh-Hans": "最新对话", "ja": "最新の返信", "ko": "최근 답글", "fr": "Dernières réponses"}
    for locale, label in labels.items():
        changes.setdefault(f"packages/views/locales/{locale}/issues.json", []).append((
            '\n  "detail": {\n', '\n  "detail": {\n' + f'    "jump_to_latest": "{label}",\n',
        ))
