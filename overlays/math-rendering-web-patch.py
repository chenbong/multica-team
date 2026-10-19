"""Extend the canonical renderer without editing the upstream checkout."""


def extend(changes):
    changes.setdefault("packages/views/rich-content/rich-content.tsx", []).extend([
        (
            'import { highlightToHtml } from "../editor/utils/highlight-markdown";\n',
            'import { highlightToHtml } from "../editor/utils/highlight-markdown";\n'
            'import { normalizeLatexDelimiters } from "./latex-delimiters";\n',
        ),
        (
            'preprocessMarkdown(content, { cdnDomain, autolinkIssueIdentifiers: true })',
            'preprocessMarkdown(normalizeLatexDelimiters(content), { cdnDomain, autolinkIssueIdentifiers: true })',
        ),
    ])
