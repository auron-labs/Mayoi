package main

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"mayoi/locales"
)

// Choose Schema… and the status bar's item of the schema of a JSON or YAML
// file: the box lists SchemaStore's schemas, and the one chosen is the
// file's in fileSchemas, by its path in the folder.

// schemaShown is the schema the status bar shows, made again as the
// settings, the catalog or the file change.
type schemaShown struct {
	path  string
	gen   [2]uint64
	label string // "" for a file whose servers take no schemas
}

// schemaLanguage is "json" or "yaml" for a document of either, else "".
func schemaLanguage(d *document) string {
	switch lang := languageOf(d); lang {
	case "json", "yaml":
		return lang
	}
	return ""
}

// schemaPath is what globs of schemas match of a document: its path in
// the folder, or its whole path without its first slash, for one outside.
func (a *app) schemaPath(d *document) string {
	if rel, ok := a.relOf(d.path); ok {
		return filepath.ToSlash(rel)
	}
	return strings.TrimPrefix(filepath.ToSlash(hostPath(d.path)), "/")
}

// takesSchemas reports whether a server of d takes schemas.
func (a *app) takesSchemas(d *document) bool {
	return d.path != "" && schemaLanguage(d) != "" &&
		slices.ContainsFunc(a.serversFor(d), func(s docServer) bool { return s.contrib.Schemas != "" })
}

// schemaOf is the schema of d's file and whether the person chose it, its
// URL "" for none.
func (a *app) schemaOf(d *document) (url string, chosen bool) {
	entries, _ := schemaCatalog()
	p := a.prefs()
	return fileSchema(a.schemaPath(d), schemaLanguage(d), p.FileSchemas, entries, p.SchemaStore)
}

// schemaLabel is the name of d's schema, as the status bar shows it; ""
// when its servers take none.
func (a *app) schemaLabel(d *document) string {
	_, catalog := schemaCatalog()
	gen := [2]uint64{a.settings.Gen(), catalog}
	if sh := &a.schemaShown; sh.path == d.path && sh.gen == gen {
		return sh.label
	}
	label := ""
	if a.takesSchemas(d) {
		// The catalog kept is read for the label, and downloaded when due.
		refreshSchemaCatalog(a.prefs().SchemaStore, false, nil)
		label = locales.SchemaNone()
		if url, _ := a.schemaOf(d); url != "" {
			label = schemaName(url)
		}
	}
	a.schemaShown = schemaShown{path: d.path, gen: gen, label: label}
	return label
}

// schemaStatus is the status bar's item of the active file's schema,
// which opens Choose Schema….
func (a *app) schemaStatus(d *document, item func(key, label, tip string, icon *ui.SVG, fn func())) {
	if label := a.schemaLabel(d); label != "" {
		item("schema", label, a.withKey(locales.CmdPluginsChooseSchema(), "plugins.chooseSchema"), nil, a.openSchemaPicker)
	}
}

// openSchemaPicker shows the box of the schemas of the active file: no
// schema, then the catalog's, the one the file has marked. Until the
// catalog is read, the box says so, and a download that failed says why
// and offers to try again.
func (a *app) openSchemaPicker() {
	d := a.activeEditor()
	if d == nil || !a.takesSchemas(d.document) {
		a.fail("%s", locales.SchemaNoFile())
		return
	}
	doc := d.document
	on := a.prefs().SchemaStore
	refreshSchemaCatalog(on, false, func() {
		a.post(func() {
			if pk := &a.pluginPick; pk.open && a.picksOpened == a.schemaPick {
				a.showSchemaPicker(doc, pk.query)
			}
		})
	})
	a.showSchemaPicker(doc, "")
}

// schemaChoice is a row of the box of the schemas: a URL, "" for none,
// or trying the download again.
type schemaChoice struct {
	url   string
	retry bool
}

// showSchemaPicker shows the box of the schemas of doc as the catalog is,
// with query typed in it.
func (a *app) showSchemaPicker(doc *document, query string) {
	lang := schemaLanguage(doc)
	match := a.schemaPath(doc)
	key := match
	if _, ok := a.relOf(doc.path); !ok {
		key = "/" + match
	}
	current, _ := a.schemaOf(doc)
	_, mine := a.prefs().FileSchemas[key]
	entries, _ := schemaCatalog()
	busy, failed := schemaCatalogState()
	var items []pickItem
	var choices []schemaChoice
	add := func(it pickItem, ch schemaChoice) {
		if ch.url == current && !ch.retry {
			it.Icon = "check"
		}
		items, choices = append(items, it), append(choices, ch)
	}
	// The file's schema comes first, chosen, so that Return keeps it.
	if current != "" {
		i := slices.IndexFunc(entries, func(e schemaEntry) bool { return e.URL == current })
		if i >= 0 {
			add(pickItem{Label: entries[i].Name, Description: entries[i].Description}, schemaChoice{url: current})
		} else {
			add(pickItem{Label: schemaName(current), Description: current}, schemaChoice{url: current})
		}
	}
	// rlsp-yaml takes no schema for a file the catalog has one for: only
	// the person's own choice can go.
	if lang == "json" || mine {
		add(pickItem{Label: locales.SchemaNone()}, schemaChoice{})
	}
	for _, e := range entries {
		if e.URL != current {
			add(pickItem{Label: e.Name, Description: e.Description}, schemaChoice{url: e.URL})
		}
	}
	var note string
	switch {
	case len(entries) > 0:
	case busy:
		note = locales.SchemaReading()
	case failed != nil && a.prefs().SchemaStore:
		note = locales.SchemaReadFailed(failed.Error())
		add(pickItem{Label: locales.SchemaTryAgain(), Icon: "refresh"}, schemaChoice{retry: true})
	case !a.prefs().SchemaStore:
		note = locales.SchemaStoreOff()
	}
	a.openPluginPicker(items, locales.SchemaPlaceholder(), func(i int) {
		if i < 0 {
			return
		}
		if ch := choices[i]; ch.retry {
			refreshSchemaCatalog(true, true, func() { a.post(func() { a.showSchemaPicker(doc, "") }) })
			a.showSchemaPicker(doc, "")
		} else {
			a.chooseSchema(key, lang, ch.url)
		}
	})
	a.schemaPick = a.picksOpened
	pk := &a.pluginPick
	pk.icon, pk.query, pk.note = iconFileCode, query, note
	// What answers at once says nothing of reading.
	if busy {
		pk.noteAfter = readingShows
	}
}

// chooseSchema makes url the schema of the files of glob, "" for none,
// which the servers take at once. For YAML, none is no choice of the
// person's.
func (a *app) chooseSchema(glob, lang, url string) {
	own := maps.Clone(a.settings.FileSchemas)
	if own == nil {
		own = map[string]string{}
	}
	if url == "" && lang == "yaml" {
		delete(own, glob)
	} else {
		own[glob] = url
	}
	if len(own) == 0 {
		own = nil
	}
	a.settings.FileSchemas = own
	a.settings.Changed()
	if plugins != nil {
		plugins.syncPrefs()
	}
	schemasChanged()
	a.invalidate()
}

// fileSchema is the schema of the file at match, the path in its folder,
// or its whole path without its first slash, for one outside it; of
// language "json" or "yaml": the person's, by the first of their globs in
// order that matches, else the first of the catalog that does when on.
// url is "" for none; chosen tells it is the person's.
func fileSchema(match, lang string, own map[string]string, entries []schemaEntry, on bool) (url string, chosen bool) {
	for _, g := range slices.Sorted(maps.Keys(own)) {
		if serverGlobs([]string{g}).MatchFile(match) {
			return own[g], true
		}
	}
	if !on {
		return "", false
	}
	for _, e := range entries {
		globs := e.jsonGlobs
		if lang == "yaml" {
			globs = e.yamlGlobs
		}
		if globs.MatchFile(match) {
			return e.URL, false
		}
	}
	return "", false
}

// schemaName is the name of a schema of url: the catalog's, or else the
// last part of its URL.
func schemaName(url string) string {
	s := &schemaStore
	s.Lock()
	i, ok := s.byURL[url]
	var name string
	if ok {
		name = s.entries[i].Name
	}
	s.Unlock()
	if ok {
		return name
	}
	url = strings.TrimRight(url, "/")
	return url[strings.LastIndex(url, "/")+1:]
}
