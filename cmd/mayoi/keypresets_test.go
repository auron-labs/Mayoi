package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/keymap"
)

// keyRuns is the command the keys run in the shortcuts, "" for none.
func keyRuns(t *testing.T, km *keymap.Map, keys string) string {
	t.Helper()
	seq, err := keymap.ParseKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := km.Lookup(seq, func(string) bool { return false })
	if b == nil {
		return ""
	}
	return b.Command
}

func onSystem(t *testing.T, os string) {
	was := hostOS
	hostOS = os
	t.Cleanup(func() { hostOS = was })
}

// Every key of a keymap is a command Mayoi has, written in keys it reads,
// and no two commands of a keymap share keys on a system.
func TestKeyPresetsAreSound(t *testing.T) {
	hs := newHarness(t, true)
	ids := map[string]bool{}
	for _, cmd := range hs.a.commands() {
		ids[cmd.id] = true
	}
	for name, table := range keyPresets {
		for _, os := range []string{"darwin", "linux"} {
			onSystem(t, os)
			taken := map[string]string{}
			for id, k := range table {
				if !ids[id] {
					t.Errorf("%s: no command %s", name, id)
				}
				if text := k.on(macKeys()); text != "" {
					if _, err := keymap.ParseKeys(text); err != nil {
						t.Errorf("%s %s %s: %v", name, os, id, err)
					}
					if other, dup := taken[text]; dup {
						t.Errorf("%s %s: %s and %s share %s", name, os, id, other, text)
					}
					taken[text] = id
				}
			}
		}
	}
}

// A keymap changes the keys of its commands, takes them from the commands
// that had them, and leaves the rest to Mayoi.
func TestKeymapOverMayoisKeys(t *testing.T) {
	onSystem(t, "darwin")
	hs := newHarness(t, true)
	cmds := map[string]command{}
	for _, cmd := range hs.a.commands() {
		cmds[cmd.id] = cmd
	}
	keysOf := func(preset, id string) string {
		return keymap.KeysString(defaultStrokes(presetKeys(preset), cmds[id]))
	}
	if got := keysOf("", "go.quickOpen"); got != "cmd+p" {
		t.Errorf("Go to File by default is %q", got)
	}
	if got := keysOf(keymapJetBrains, "go.quickOpen"); got != "shift+cmd+o" {
		t.Errorf("Go to File in JetBrains is %q", got)
	}
	// Open Folder had ⌘O, which JetBrains' Go to Symbol takes.
	if got := keysOf(keymapJetBrains, "file.openFolder"); got != "" {
		t.Errorf("Open Folder in JetBrains is %q", got)
	}
	// A command the keymap leaves be keeps its keys.
	if got := keysOf(keymapJetBrains, "file.save"); got != "cmd+s" {
		t.Errorf("Save in JetBrains is %q", got)
	}
	onSystem(t, "windows")
	if got := keysOf(keymapJetBrains, "sel.deleteLine"); got != "ctrl+y" {
		t.Errorf("Delete Line in JetBrains on Windows is %q", got)
	}
	if got := keysOf(keymapVSCode, "view.selectTheme"); got != "ctrl+k ctrl+t" {
		t.Errorf("Color Theme in VS Code on Windows is %q", got)
	}
}

// The keys of keybindings.json win over a keymap's, and over Mayoi's own.
func TestUserKeysWinOverAKeymap(t *testing.T) {
	onSystem(t, "darwin")
	user, err := keymap.Parse([]byte(`[
		{"key": "cmd+d", "command": "view.sidebar"},
		{"key": "cmd+e", "command": "-file.openRecent"},
		{"key": "cmd+b", "command": "-go.definition"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if layerKeys("", user) != user {
		t.Error("no keymap changed the file's shortcuts")
	}
	km := layerKeys(keymapJetBrains, user)
	for keys, want := range map[string]string{
		"cmd+shift+o": "go.quickOpen", // the keymap's
		"cmd+d":       "view.sidebar", // the file's, over the keymap's Duplicate Line
		"cmd+e":       "",             // the file took it away
		"cmd+b":       "",
	} {
		if got := keyRuns(t, km, keys); got != want {
			t.Errorf("%s runs %q, not %q", keys, got, want)
		}
	}
	again, _ := keymap.Parse([]byte(`[{"key": "cmd+p", "command": "go.quickOpen"}]`))
	if got := keyRuns(t, layerKeys(keymapJetBrains, again), "cmd+p"); got != "go.quickOpen" {
		t.Errorf("the file's keys for a keymap command run %q", got)
	}
	if !km.Binds(keymap.Stroke{Mods: ui.Super | ui.Shift, Key: ui.KeyO}) {
		t.Error("the keymap's keys are not bound")
	}
}

// A keymap changed in the settings applies to the windows at once, with
// the file's keys still over it.
func TestKeymapSettingAppliesLive(t *testing.T) {
	onSystem(t, "darwin")
	ph := newPrefsHarness(t)
	user, _ := keymap.Parse([]byte(`[{"key": "cmd+d", "command": "view.sidebar"}]`))
	ph.m.keys = user
	ph.m.applyKeys()
	if keyRuns(t, ph.a.keys, "cmd+shift+o") != "" {
		t.Fatal("a keymap before choosing one")
	}
	ph.m.s.Keymap = keymapJetBrains
	ph.m.prefsChanged()
	got := []string{keyRuns(t, ph.a.keys, "cmd+shift+o"), keyRuns(t, ph.a.keys, "cmd+d")}
	if !slices.Equal(got, []string{"go.quickOpen", "view.sidebar"}) {
		t.Errorf("after choosing JetBrains: %q", got)
	}
}
