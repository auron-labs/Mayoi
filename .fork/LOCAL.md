# Fork behavior

## Plugin-free language servers

Users can configure language servers in their personal settings for a language or project-relative glob, including on SSH hosts. The first matching user server takes precedence, and using a plugin server's ID replaces that server. Folder settings cannot choose a program to execute.

- Paths: `cmd/mayoi/lsp.go`, `cmd/mayoi/main.go`, `internal/settings/languageservers.go`, `docs/SETTINGS.md`
- Check: `go test ./internal/settings -run 'Test(ValidLanguageServers|LanguageServersPreference)'` and `go test ./cmd/mayoi -run 'Test(SelectServer|ServerForPathsFromTheFolder|UserLanguageServerReplacesAPlugin)'`
- Upstream changes: keep the `languageServers` list and precedence. Use `languageServerOptions` for the separate map of initialization overrides by server ID.

## Search Everywhere

Double Shift and the Go menu find file names, text, and workspace symbols in one picker. The picker retains its query, supports opening to the side, and can hand off file and text results to Search.

- Paths: `cmd/mayoi/everywhere.go`, `cmd/mayoi/search.go`, `cmd/mayoi/remotesearch.go`, `docs/FEATURES.md`
- Check: `go test ./cmd/mayoi -run 'Test(ShiftTwiceOpensSearchEverywhere|SearchEverywhere)'`
- Upstream changes: keep the combined picker and its keyboard and Search view integration when taking search or navigation changes.

## Multiple carets

Users can toggle carets with Alt-click, select a column with Alt and middle drag, edit and run supported line commands at all carets, copy and cut from the Edit menu, and undo edits with the caret set restored. Vertical movement follows wrapped rows.

- Paths: `cmd/mayoi/multicursor.go`, `cmd/mayoi/editing.go`, `cmd/mayoi/history.go`, `cmd/mayoi/commands.go`, `docs/FEATURES.md`
- Check: `go test ./cmd/mayoi -run 'Test(AltClick|Caret|MultiCopyCutAndPaste|ColumnSelectByDrag|LineCommandsRunOnceALine)'`
- Upstream changes: adopt upstream multiple-caret commands and fixes while preserving existing mouse gestures, wrapped-row movement, line commands, and clipboard and undo behavior.

## Tab names and mouse navigation

Tabs with the same file name show enough of their parent paths to tell them apart. Mouse back and forward buttons navigate history from every pane.

- Paths: `cmd/mayoi/tabs.go`, `cmd/mayoi/commands.go`, `cmd/mayoi/editing.go`
- Check: `go test ./cmd/mayoi -run 'Test(FolderSuffixes|MouseButtonsGoThroughHistory)'`
- Upstream changes: preserve tab disambiguation and pane-wide history navigation.

## Word and line deletion

Word deletion stops at punctuation and line boundaries. macOS users can opt into deleting the whole line with Cmd+Backspace.

- Paths: `cmd/mayoi/editing.go`, `cmd/mayoi/multicursor.go`, `internal/settings/settings.go`
- Check: `go test ./cmd/mayoi -run 'Test(DeleteWordStopsAtPunctuationAndLineEdges|CmdBackspaceDeletesLine)'`
- Upstream changes: keep these deletion rules and the opt-in setting.

## File associations

Users can set languages by file name or glob for highlighting and language server selection. Explicit associations take precedence over plugin associations.

- Paths: `cmd/mayoi/associations.go`, `cmd/mayoi/plugins.go`, `internal/highlight/associations.go`, `internal/settings/settings.go`
- Check: `go test ./internal/highlight ./internal/settings -run 'Test(Associations|FileAssociations)'`
- Upstream changes: keep explicit associations and their precedence.

## Keymap presets

Users can choose Mayoi, VS Code, or JetBrains shortcuts without restarting. Personal bindings override presets, Vim mode works with each preset, and Keyboard Shortcuts reflects the active preset. Cmd shortcuts on macOS leave the editor caret ready for input.

- Paths: `cmd/mayoi/keypresets.go`, `cmd/mayoi/keymap.go`, `cmd/mayoi/prefskeys.go`, `internal/settings/settings.go`
- Check: `go test ./cmd/mayoi -run 'Test(KeyPresets|Keymap|UserKeysWinOverAKeymap)'`
- Upstream changes: preserve preset precedence, active shortcuts in settings, and editor focus.
