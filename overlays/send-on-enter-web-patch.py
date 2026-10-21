"""Default the `send` shortcut to plain Enter instead of Mod+Enter.

Upstream already supports plain Enter as a user-chosen Send binding: rich
editors replay their native Enter on Shift+Enter (paragraphs, lists, code
blocks), plain textareas keep Shift+Enter as a newline, IME composition and
key repeat are guarded, and single-line title editors refuse plain Enter.
Only the shipped default changes here; users can still pick Mod+Enter in
Settings → Keyboard shortcuts.
"""

DEFINITIONS = "packages/core/shortcuts/definitions.ts"
STORE_TEST = "packages/core/shortcuts/store.test.ts"
TITLE_TEST = "packages/views/editor/title-editor.shortcut.test.tsx"
RUN_CONFIRM_TEST = "packages/views/modals/run-confirm.test.tsx"
WAKEUP_TEST = "packages/views/issues/components/wakeup-create.test.tsx"
SETTINGS_TEST = "packages/views/settings/components/keyboard-shortcuts-tab.test.tsx"


def extend(changes):
    changes.setdefault(DEFINITIONS, []).append((
        '  { id: "send", category: "general", defaultShortcut: primary("Enter"), allowInEditable: true },\n',
        '  // Local overlay: plain Enter sends; Shift+Enter keeps the newline behavior.\n'
        '  { id: "send", category: "general", defaultShortcut: createShortcutChord("Enter"), allowInEditable: true },\n',
    ))

    changes.setdefault(STORE_TEST, []).extend([
        (
            '    expect(getShortcut("send")).toEqual(\n'
            '      createShortcutChord("Enter", { primary: true }),\n'
            '    );\n'
            '\n'
            '    const custom = ',
            '    expect(getShortcut("send")).toEqual(createShortcutChord("Enter"));\n'
            '\n'
            '    const custom = ',
        ),
        (
            '      createShortcutChord("Enter", { primary: true, shift: true }),\n'
            '    );\n'
            '    expect(getShortcut("send")).toEqual(\n'
            '      createShortcutChord("Enter", { primary: true }),\n'
            '    );\n',
            '      createShortcutChord("Enter", { primary: true, shift: true }),\n'
            '    );\n'
            '    expect(getShortcut("send")).toEqual(createShortcutChord("Enter"));\n',
        ),
    ])

    # The title editor never submits on plain Enter (#5532), so the chord test
    # now opts into Mod+Enter explicitly instead of relying on the default.
    changes.setdefault(TITLE_TEST, []).extend([
        (
            '  it("fires onSubmitShortcut on the default Cmd+Enter chord", async () => {\n',
            '  it("fires onSubmitShortcut on a Cmd+Enter send chord", async () => {\n'
            '    useShortcutStore.setState({\n'
            '      overrides: { send: createShortcutChord("Enter", { primary: true }) },\n'
            '    });\n',
        ),
        (
            '  it("leaves plain Enter to the keymap, so it never creates", async () => {\n',
            '  it("leaves plain Enter (the default send) to the keymap, so it never creates", async () => {\n',
        ),
    ])

    # These cases exercise the ⌘+Enter chord itself; pin it per test so the
    # plain-Enter default does not change what they cover.
    changes.setdefault(RUN_CONFIRM_TEST, []).append((
        '  configureShortcutPlatform("macos");\n'
        '  useShortcutStore.setState({ overrides: {} });\n'
        '});\n',
        '  configureShortcutPlatform("macos");\n'
        '  useShortcutStore.setState({\n'
        '    overrides: { send: createShortcutChord("Enter", { primary: true }) },\n'
        '  });\n'
        '});\n',
    ))

    changes.setdefault(WAKEUP_TEST, []).extend([
        ('  it("submits with Cmd+Enter", async () => {\n',
         '  it("submits with Enter", async () => {\n'),
        ('    fireEvent.keyDown(input, { key: "Enter", metaKey: true });\n'
         '    await waitFor(() => expect(create).toHaveBeenCalledWith(',
         '    fireEvent.keyDown(input, { key: "Enter" });\n'
         '    await waitFor(() => expect(create).toHaveBeenCalledWith('),
    ])

    changes.setdefault(SETTINGS_TEST, []).append((
        '    fireEvent.keyDown(sendRecorder, { key: "Enter", shiftKey: true });\n'
        '    expect(getShortcut("send")).toEqual(\n'
        '      createShortcutChord("Enter", { primary: true }),\n'
        '    );\n',
        '    fireEvent.keyDown(sendRecorder, { key: "Enter", shiftKey: true });\n'
        '    expect(getShortcut("send")).toEqual(createShortcutChord("Enter"));\n',
    ))
