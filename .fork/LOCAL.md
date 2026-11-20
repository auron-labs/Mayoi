# Fork behavior

## Plugin-free language servers

Users can configure language servers in their personal settings for a language or project-relative glob, including on SSH hosts. The first matching user server takes precedence, and using a plugin server's ID replaces that server. Folder settings cannot choose a program to execute.

- Paths: `cmd/mayoi/lsp.go`, `cmd/mayoi/main.go`, `internal/settings/languageservers.go`, `docs/SETTINGS.md`
- Check: `go test ./internal/settings -run 'Test(ValidLanguageServers|LanguageServersPreference)'` and `go test ./cmd/mayoi -run 'Test(SelectServer|ServerForPathsFromTheFolder|UserLanguageServerReplacesAPlugin)'`
- Upstream changes: keep the `languageServers` list and precedence. Use `languageServerOptions` for the separate map of initialization overrides by server ID.

## Search Everywhere

Double Shift and the Go menu find files by name and text in one picker. The picker retains its query, supports opening to the side, and can hand off all results to Search.

- Paths: `cmd/mayoi/everywhere.go`, `cmd/mayoi/search.go`, `cmd/mayoi/remotesearch.go`, `docs/FEATURES.md`
- Check: `go test ./cmd/mayoi -run 'Test(ShiftTwiceOpensSearchEverywhere|SearchEverywhere)'`
- Upstream changes: keep the combined picker and its keyboard and Search view integration when taking search or navigation changes.

## Multiple carets

Users can toggle carets with Alt-click, select a column with Alt and middle drag, edit and run supported line commands at all carets, copy and cut from the Edit menu, and undo edits with the caret set restored. Vertical movement follows wrapped rows.

- Paths: `cmd/mayoi/multicursor.go`, `cmd/mayoi/editing.go`, `cmd/mayoi/history.go`, `cmd/mayoi/commands.go`, `docs/FEATURES.md`
- Check: `go test ./cmd/mayoi -run 'Test(AltClick|Caret|MultiCopyCutAndPaste|ColumnSelectByDrag|LineCommandsRunOnceALine)'`
- Upstream changes: adopt upstream multiple-caret commands and fixes while preserving existing mouse gestures, wrapped-row movement, line commands, and clipboard and undo behavior.
