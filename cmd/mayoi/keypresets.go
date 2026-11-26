package main

import (
	"maps"
	"slices"
	"strings"

	"mayoi/internal/keymap"
)

// A keymap is the editor whose shortcuts Mayoi's follow, the keymap
// setting: its table of keys, over Mayoi's own and under keybindings.json.
const (
	keymapVSCode    = "vscode"
	keymapJetBrains = "jetbrains"
)

// presetKey is the keys of a command in a keymap, as keybindings.json
// writes them, on macOS and on Windows and Linux. A platform with none
// leaves the command its own keys there.
type presetKey struct{ mac, other string }

// both is a key that is the same on every system, with Control for
// Command on Windows and Linux.
func both(mac string) presetKey {
	return presetKey{mac, strings.ReplaceAll(mac, "cmd", "ctrl")}
}

// on is the keys of k on the system.
func (k presetKey) on(mac bool) string {
	if mac {
		return k.mac
	}
	return k.other
}

// keyPresets are the keys each keymap changes, by command, for the
// commands Mayoi has: the high-value ones, not every key of the editor.
var keyPresets = map[string]map[string]presetKey{
	// VS Code's own keys are Mayoi's, so this is what Mayoi leaves out:
	// the chords of its keys, and the keys Windows and Linux have none for.
	keymapVSCode: {
		"sel.lineBelow":      both("cmd+enter"),
		"sel.joinLines":      {mac: "ctrl+j"},
		"go.hover":           both("cmd+k cmd+i"),
		"file.keybindings":   both("cmd+k cmd+s"),
		"file.closeFolder":   both("cmd+k f"),
		"file.saveAll":       {other: "ctrl+k s"},
		"view.previewToSide": both("cmd+k v"),
		"view.selectTheme":   both("cmd+k cmd+t"),
		"view.languageMode":  both("cmd+k m"),
		"view.nextColumn":    both("cmd+k cmd+right"),
		"view.prevColumn":    both("cmd+k cmd+left"),
	},
	keymapJetBrains: {
		"go.quickOpen":             {"cmd+shift+o", "ctrl+shift+n"},
		"go.symbol":                {"cmd+o", "ctrl+n"},
		"go.commands":              both("cmd+shift+a"),
		"go.definition":            both("cmd+b"),
		"go.line":                  {"cmd+l", "ctrl+g"},
		"go.bracket":               {"ctrl+m", "ctrl+shift+m"},
		"go.hover":                 {"ctrl+j", "ctrl+q"},
		"file.openRecent":          both("cmd+e"),
		"file.rename":              both("shift+f6"),
		"edit.replace":             both("cmd+r"),
		"edit.replaceInFiles":      both("cmd+shift+r"),
		"sel.comment":              both("cmd+/"),
		"sel.copyDown":             both("cmd+d"),
		"sel.deleteLine":           {"cmd+backspace", "ctrl+y"},
		"sel.moveUp":               {"cmd+shift+up", "alt+shift+up"},
		"sel.moveDown":             {"cmd+shift+down", "alt+shift+down"},
		"sel.joinLines":            both("ctrl+shift+j"),
		"sel.lineBelow":            both("shift+enter"),
		"sel.lineAbove":            {"cmd+alt+enter", "ctrl+alt+enter"},
		"sel.format":               {"cmd+alt+l", "ctrl+alt+l"},
		"sel.addNextOccurrence":    {"ctrl+g", "alt+j"},
		"sel.selectAllOccurrences": {"cmd+ctrl+g", "ctrl+alt+shift+j"},
		"view.sidebar":             {"cmd+1", "alt+1"},
		"view.terminal":            both("alt+f12"),
		"git.commit":               both("cmd+k"),
		"git.push":                 both("cmd+shift+k"),
		"git.pull":                 both("cmd+t"),
	},
}

// presetKeys are the keys a keymap gives its commands on the system, by
// command. A keymap Mayoi has none of, and "", give none.
func presetKeys(keymapName string) map[string][]keymap.Stroke {
	out := map[string][]keymap.Stroke{}
	for id, k := range keyPresets[keymapName] {
		keys, err := keymap.ParseKeys(k.on(macKeys()))
		if err == nil {
			out[id] = keys
		}
	}
	return out
}

// layerKeys is the shortcuts Mayoi runs: the keys of the keymap over
// Mayoi's own, and keybindings.json's over those, as a later binding wins
// over an earlier one. A keymap command first loses its own keys, with
// the removal of a binding of the file.
func layerKeys(keymapName string, user *keymap.Map) *keymap.Map {
	preset := presetKeys(keymapName)
	if len(preset) == 0 {
		return user
	}
	layered := &keymap.Map{}
	for _, id := range slices.Sorted(maps.Keys(preset)) {
		layered.Bindings = append(layered.Bindings,
			keymap.Binding{Command: "-" + id},
			keymap.Binding{Keys: preset[id], Command: id})
	}
	if user != nil {
		layered.Bindings = append(layered.Bindings, user.Bindings...)
		layered.Errors = user.Errors
	}
	return layered
}

// defaultStrokes are the keys a command has before keybindings.json: its
// keymap's, or its own unless a keymap command takes them. preset is
// presetKeys of the keymap.
func defaultStrokes(preset map[string][]keymap.Stroke, cmd command) []keymap.Stroke {
	if keys, ok := preset[cmd.id]; ok {
		return keys
	}
	st, ok := parseAccel(defaultAccel(cmd))
	if !ok {
		return nil
	}
	for _, keys := range preset {
		if keys[0] == st {
			return nil
		}
	}
	return []keymap.Stroke{st}
}
