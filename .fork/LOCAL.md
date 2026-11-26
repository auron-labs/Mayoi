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

Tabs with the same file name show enough of their parent paths to tell them apart. Mouse back and forward buttons navigate history from every pane. The history keeps the place left whenever the editor with the keyboard shows another file, by any route, and when the caret jumps ten or more lines through one file without an edit.

- Paths: `cmd/mayoi/tabs.go`, `cmd/mayoi/commands.go`, `cmd/mayoi/editing.go`, `cmd/mayoi/navhistory.go`, `cmd/mayoi/app.go` (`tick` calls `navWatch`), `cmd/mayoi/diffview.go` (`openFileAt`)
- Check: `go test ./cmd/mayoi -run 'Test(FolderSuffixes|MouseButtonsGoThroughHistory|BackThroughFilesOpened|BackAcrossTabs|BackFromAJumpInAFile|BackFromATabOfNoFile|NavigationHistory)'`
- Upstream changes: preserve tab disambiguation, pane-wide history navigation, and the once-a-frame recording of places left; history's own moves and explicit pushes reset `nav.seen` so they don't push again.

## Word and line deletion

Word deletion stops at punctuation and line boundaries. macOS users can opt into deleting the whole line with Cmd+Backspace.

- Paths: `cmd/mayoi/editing.go`, `cmd/mayoi/multicursor.go`, `internal/settings/settings.go`
- Check: `go test ./cmd/mayoi -run 'Test(DeleteWordStopsAtPunctuationAndLineEdges|CmdBackspaceDeletesLine)'`
- Upstream changes: keep these deletion rules and the opt-in setting.

## File associations

Users can set languages by file name or glob for highlighting and language server selection. Explicit associations take precedence over plugin associations. The status bar's language opens a picker that sets the file's language for the session, goes back to Auto Detect, or writes an association for the file's extension or name, which every open matching file takes at once.

- Paths: `cmd/mayoi/associations.go`, `cmd/mayoi/languagepicker.go`, `cmd/mayoi/plugins.go`, `internal/highlight/associations.go`, `internal/settings/settings.go`
- Check: `go test ./internal/highlight ./internal/settings -run 'Test(Associations|FileAssociations|LanguagesRoundTrip)'` and `go test ./cmd/mayoi -run 'Test(LanguagePicker|AssociationGlobs)'`
- Upstream changes: keep explicit associations and their precedence, and the picker's command `view.languageMode` with the VS Code keymap's `cmd+k m`.

## Keymap presets

Users can choose Mayoi, VS Code, or JetBrains shortcuts without restarting. Personal bindings override presets, Vim mode works with each preset, and Keyboard Shortcuts reflects the active preset. Cmd shortcuts on macOS leave the editor caret ready for input.

- Paths: `cmd/mayoi/keypresets.go`, `cmd/mayoi/keymap.go`, `cmd/mayoi/prefskeys.go`, `internal/settings/settings.go`
- Check: `go test ./cmd/mayoi -run 'Test(KeyPresets|Keymap|UserKeysWinOverAKeymap|JetBrainsNextOccurrence)'`
- Upstream changes: preserve preset precedence, active shortcuts in settings, and editor focus.

## Line breaks in find

Ctrl+Enter, on every system as in VS Code, inserts a line break at the caret in the find and replace fields, where matches may span lines: the editor and the Markdown preview. Return and Shift+Return still step and replace, and a diff's find keeps Ctrl+Enter stepping because it matches line by line. The find fields keep these Return keys from keymap presets, whose Insert Line Below would otherwise edit the document.

- Paths: `cmd/mayoi/find.go`, `cmd/mayoi/markdownfind.go`
- Check: `go test ./cmd/mayoi -run 'TestFind(CtrlEnterBreaksTheLine|KeepsReturnFromTheKeymap|FindsAMultilineSelection|ShiftEnterGoesBack)'`
- Upstream changes: keep Ctrl+Enter breaking the line, and the find fields' Return keys away from the keymap, when taking changes to the find widget's keys or fields.

## JSON and YAML schemas

JSON and YAML files get SchemaStore's schema by their names, its catalog downloaded in the background once a day and kept in the cache folder for offline use. Users choose a file's schema, or none, with Choose Schema… or the status bar, kept by glob in `fileSchemas`, which wins over the catalog. Servers declaring `schemas` in their plugin manifest are sent the associations through `workspace/didChangeConfiguration` as they start and whenever the catalog or `fileSchemas` changes; `schemaStore` turns the download off.

- Paths: `cmd/mayoi/schemastore.go`, `cmd/mayoi/schemapicker.go`, `cmd/mayoi/lsp.go`, `cmd/mayoi/pluginpick.go`, `internal/plugin/manifest.go`, `internal/settings/settings.go`, `plugins/vscode-json-language-server/plugin.json`, `plugins/rlsp-yaml/plugin.json`
- Check: `go test ./cmd/mayoi -run 'Test(SchemaSettings|SchemaCatalogDownloads|ChooseSchema)'`
- Upstream changes: keep the `schemas` manifest field on the JSON and YAML servers and the settings each server expects: vscode-json-language-server's `json.schemas` with `json.validate.enable`, and rlsp-yaml's full settings object.

## Explorer refresh, moves and deletes

Users can refresh one folder of the explorer, and what is read inside it, from its menu, locally and on SSH hosts. Dragging a row chosen with others moves all of them, skipping those already there or that would go into themselves, and the files among them open together on the editor.

- Paths: `cmd/mayoi/sidebar.go`, `cmd/mayoi/explorermove.go`, `cmd/mayoi/explorerselect.go`, `internal/workspace/tree.go`
- Check: `go test ./internal/workspace -run TestRefreshInReadsAFolderAgain` and `go test ./cmd/mayoi -run 'Test(ExplorerRefreshesA|ExplorerDrag)'`
- Upstream changes: keep the folder menu's **Refresh** and `Tree.RefreshIn`, and moves of every row chosen (`treePaths`, `outermost`), when taking explorer or tree changes.
