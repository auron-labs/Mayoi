package main

import (
	"cmp"
	"maps"
	"path/filepath"
	"strings"

	"mayoi/internal/highlight"
	"mayoi/internal/plugin"
	"mayoi/locales"
)

// languageChoice is a language of the box of languages: its ID, as
// languageOf has it, its name to show, and the plugin that declares it,
// nil for Mayoi's own.
type languageChoice struct {
	id, name string
	owner    *plugin.Plugin
	about    string // what the box says beside the name: the plugin's, or else the ID
}

// languageChoices are the languages a file can have: the highlighter's,
// then those the plugins declare that it does not have. Only the
// highlighter's are with plugins, as the setting names those alone.
func languageChoices(withPlugins bool) []languageChoice {
	var list []languageChoice
	index := map[string]int{}
	for _, l := range highlight.Languages() {
		id := highlight.ID(l)
		index[id] = len(list)
		list = append(list, languageChoice{id: id, name: highlight.DisplayName(l)})
	}
	if !withPlugins || plugins == nil {
		return list
	}
	for _, p := range plugins.enabled() {
		for _, l := range p.Contributes.Languages {
			id := strings.ToLower(l.ID)
			if i, ok := index[id]; ok {
				list[i].owner = p
				continue
			}
			index[id] = len(list)
			list = append(list, languageChoice{id: id, name: l.ID, owner: p, about: cmp.Or(p.DisplayName, p.Name)})
		}
	}
	return list
}

// associationGlobs are the globs the box offers to give every file like
// path a language: its extension's, and its name's, when the extension is
// none or one no language has, as Dockerfile.dev.
func associationGlobs(path string) []string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext == "" || ext == base {
		return []string{base}
	}
	globs := []string{"*" + ext}
	if highlight.Lexer("file"+ext) == nil {
		globs = append(globs, base)
	}
	return globs
}

// openLanguagePicker shows the box of the languages of the file shown,
// on the one it has.
func (a *app) openLanguagePicker() {
	ed := a.activeEditor()
	if ed == nil {
		return
	}
	d := ed.document
	choices := languageChoices(true)
	current := languageOf(d)
	items := []pickItem{{Label: locales.LanguageAutoDetect()}}
	var globs []string
	if d.path != "" {
		globs = associationGlobs(d.path)
		for _, g := range globs {
			items = append(items, pickItem{Label: locales.LanguageAssociate(g)})
		}
	}
	extra := len(items)
	first := 0
	for i, ch := range choices {
		it := pickItem{Label: ch.name, Description: cmp.Or(ch.about, ch.id)}
		if ch.id == current {
			it.Description, first = locales.LanguageCurrent(ch.id), extra+i
		}
		items = append(items, it)
	}
	a.openPluginPicker(items, locales.LanguagePick(), func(i int) {
		switch {
		case i < 0 || !containsDoc(a.docs, d):
		case i == 0:
			a.detectLanguage(d)
			a.languageChanged(d)
		case i < extra:
			a.openAssociationPicker(d, globs[i-1])
		default:
			ch := choices[i-extra]
			a.setDocumentLanguage(d, ch.owner, ch.id)
			a.languageChanged(d)
		}
	})
	a.pluginPick.icon, a.pluginPick.first = iconFileCode, first
}

// openAssociationPicker asks the language that files matching glob have,
// on that of d.
func (a *app) openAssociationPicker(d *document, glob string) {
	choices := languageChoices(false)
	current := languageOf(d)
	items := make([]pickItem, len(choices))
	first := 0
	for i, ch := range choices {
		items[i] = pickItem{Label: ch.name, Description: ch.id}
		if ch.id == current {
			items[i].Description, first = locales.LanguageCurrent(ch.id), i
		}
	}
	a.openPluginPicker(items, locales.LanguageAssociatePick(glob), func(i int) {
		if i >= 0 {
			a.associateLanguage(d, glob, choices[i].id)
		}
	})
	a.pluginPick.icon, a.pluginPick.first = iconFileCode, first
}

// associateLanguage gives the files that match glob the language in the
// person's settings, and each file open in a window its language from
// then: d too, rather than one chosen for it.
func (a *app) associateLanguage(d *document, glob, language string) {
	m := maps.Clone(a.settings.FileAssociations)
	if m == nil {
		m = map[string]string{}
	}
	m[glob] = language
	a.settings.FileAssociations = m
	a.settings.Changed()
	projects := a.host.everyProject()
	if len(projects) == 0 {
		projects = []*app{a}
	}
	was := map[*document]string{}
	for _, p := range projects {
		for _, doc := range p.docs {
			was[doc] = languageOf(doc)
		}
	}
	highlight.SetAssociations(m)
	// rehighlight leaves a language chosen for a file, as it should for
	// every file but the one the glob is of.
	d.language, d.languageOwner, d.lexerOwner = "", "", nil
	for _, p := range projects {
		p.rehighlight()
		for _, doc := range p.docs {
			if languageOf(doc) != was[doc] {
				p.languageChanged(doc)
			}
		}
		p.invalidate()
	}
}

// detectLanguage gives a document the language of its file's name again,
// rather than one chosen for it.
func (a *app) detectLanguage(d *document) {
	d.language, d.languageOwner, d.lexerOwner = "", "", nil
	d.hl.Close()
	d.hl = highlight.New(highlight.Lexer(d.path), a.invalidate)
}

// languageChanged has the language servers and the plugins of a
// document's language take it, as its language changed: those of the one
// before let it go.
func (a *app) languageChanged(d *document) {
	if plugins == nil {
		return
	}
	plugins.reg.Activate("onLanguage:" + languageOf(d))
	a.lspDoc("close", d)
	a.lspDoc("open", d)
}
