package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"mayoi/internal/lsp"
	"mayoi/internal/workspace"
)

// The schemas of JSON and YAML files: SchemaStore's catalog, downloaded in
// the background once a day and kept beside Mayoi's cache, gives a file a
// schema by its name, and fileSchemas the person's own, which win. The
// servers that take schemas (a plugin's "schemas") are sent them as they
// start, and again as the catalog or the choices change.

// schemaCatalogURL is where SchemaStore's catalog downloads from.
const schemaCatalogURL = "https://www.schemastore.org/api/json/catalog.json"

// schemaCatalogMax is the most of the catalog read, many times its size.
const schemaCatalogMax = 16 << 20

// schemaCatalogAge is how long the catalog is used before it is
// downloaded again, and schemaCatalogRetry how long after a download
// failed it is not tried again unless asked.
const (
	schemaCatalogAge   = 24 * time.Hour
	schemaCatalogRetry = 5 * time.Minute
)

// schemaEntry is a schema of the catalog.
type schemaEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	FileMatch   []string `json:"fileMatch"`
	URL         string   `json:"url"`
	// json and yaml are the globs of FileMatch for JSON files, without
	// those of YAML files, and for YAML files, without those of JSON files.
	json, yaml []string
	// jsonGlobs and yamlGlobs match them as the servers do.
	jsonGlobs, yamlGlobs workspace.Globs
}

// schemaStore is the catalog as it was last read.
var schemaStore = struct {
	sync.Mutex
	entries []schemaEntry
	byURL   map[string]int
	// gen counts the catalogs read, for what is made of one.
	gen      uint64
	read     bool      // the file was read, or there was none
	at       time.Time // when the catalog was downloaded, zero for never
	err      error     // why the last download failed
	failedAt time.Time
	busy     bool
	arrived  []func()
}{}

// schemaCatalogFile is where the catalog is kept, "" for nowhere.
var schemaCatalogFile = func() string {
	dir, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "schemastore-catalog.json")
}

var schemaHTTP = &http.Client{Timeout: 30 * time.Second}

// fetchSchemaCatalog downloads the catalog; the tests have it fail rather
// than go out.
var fetchSchemaCatalog = func(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", schemaCatalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := schemaHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, schemaCatalogMax+1))
	if err == nil && len(data) > schemaCatalogMax {
		err = errors.New("the catalog is too large")
	}
	return data, err
}

// parseSchemaCatalog reads a catalog, its entries without a URL left out.
func parseSchemaCatalog(data []byte) ([]schemaEntry, error) {
	var c struct {
		Schemas []schemaEntry `json:"schemas"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("the catalog does not read: %w", err)
	}
	entries := slices.DeleteFunc(c.Schemas, func(e schemaEntry) bool { return e.URL == "" || e.Name == "" })
	for i := range entries {
		e := &entries[i]
		for _, g := range e.FileMatch {
			yaml, json := globExt(g, ".yml", ".yaml"), globExt(g, ".json", ".jsonc", ".json5")
			if !yaml {
				e.json = append(e.json, g)
			}
			if !json {
				e.yaml = append(e.yaml, g)
			}
		}
		e.jsonGlobs, e.yamlGlobs = serverGlobs(e.json), serverGlobs(e.yaml)
	}
	return entries, nil
}

// globExt reports whether a glob matches only names of one of exts.
func globExt(glob string, exts ...string) bool {
	return slices.ContainsFunc(exts, func(ext string) bool { return strings.HasSuffix(strings.ToLower(glob), ext) })
}

// serverGlobs matches globs as both servers do: at any depth, from the
// start of a folder's or a file's name; "!" globs are left out.
func serverGlobs(globs []string) workspace.Globs {
	var out []string
	for _, g := range globs {
		if g = strings.TrimPrefix(g, "/"); g != "" && !strings.HasPrefix(g, "!") {
			if !strings.HasPrefix(g, "**/") {
				g = "**/" + g
			}
			out = append(out, g)
		}
	}
	return workspace.ParseGlobs(strings.Join(out, ","))
}

// setSchemaCatalog makes entries the catalog, downloaded at.
func setSchemaCatalog(entries []schemaEntry, at time.Time) {
	s := &schemaStore
	s.entries, s.at = entries, at
	s.byURL = make(map[string]int, len(entries))
	for i, e := range entries {
		if _, ok := s.byURL[e.URL]; !ok {
			s.byURL[e.URL] = i
		}
	}
	s.gen++
}

// loadSchemaCatalog reads the catalog kept, the first time, and waits for
// it: a server that starts is sent the schemas of the catalog it finds.
func loadSchemaCatalog() {
	s := &schemaStore
	s.Lock()
	defer s.Unlock()
	if s.read {
		return
	}
	s.read = true
	path := schemaCatalogFile()
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// A damaged file is downloaded again.
	if entries, err := parseSchemaCatalog(data); err == nil {
		setSchemaCatalog(entries, info.ModTime())
	}
}

// schemaCatalog is the catalog read, nil before it is, and its gen.
func schemaCatalog() ([]schemaEntry, uint64) {
	s := &schemaStore
	s.Lock()
	defer s.Unlock()
	return s.entries, s.gen
}

// schemaCatalogState is how the catalog is: being read or downloaded, and
// why its last download failed, nil when it did not.
func schemaCatalogState() (busy bool, err error) {
	s := &schemaStore
	s.Lock()
	defer s.Unlock()
	return s.busy || !s.read, s.err
}

// refreshSchemaCatalog reads the catalog kept, in the background, and
// downloads it when download is on and the one kept is a day old, or there
// is none: not again for a while after it failed, unless retry. arrived,
// unless nil, is called once that is done, downloaded or not; the
// servers that take schemas, and the windows, are told of a new catalog.
func refreshSchemaCatalog(download, retry bool, arrived func()) {
	s := &schemaStore
	s.Lock()
	if arrived != nil {
		s.arrived = append(s.arrived, arrived)
	}
	if s.busy {
		s.Unlock()
		return
	}
	s.busy = true
	s.Unlock()
	go func() {
		defer recoverCrash()
		loadSchemaCatalog()
		s.Lock()
		gen := s.gen
		due := download && (s.at.IsZero() || time.Since(s.at) >= schemaCatalogAge) &&
			(retry || s.failedAt.IsZero() || time.Since(s.failedAt) >= schemaCatalogRetry)
		s.Unlock()
		if due {
			downloadSchemaCatalog()
		}
		s.Lock()
		s.busy = false
		calls := s.arrived
		s.arrived = nil
		changed := s.gen != gen
		s.Unlock()
		if changed {
			schemasChanged()
		}
		for _, f := range calls {
			f()
		}
	}()
}

// downloadSchemaCatalog downloads the catalog, and keeps it.
func downloadSchemaCatalog() {
	s := &schemaStore
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	data, err := fetchSchemaCatalog(ctx)
	var entries []schemaEntry
	if err == nil {
		entries, err = parseSchemaCatalog(data)
	}
	s.Lock()
	defer s.Unlock()
	if err != nil {
		s.err, s.failedAt = err, time.Now()
		return
	}
	s.err, s.failedAt = nil, time.Time{}
	setSchemaCatalog(entries, time.Now())
	if path := schemaCatalogFile(); path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		tmp := path + ".tmp"
		if os.WriteFile(tmp, data, 0o644) == nil {
			os.Rename(tmp, path)
		}
	}
}

// schemasChanged sends the servers that take schemas theirs anew, and
// draws the windows again, as the catalog or the person's choices changed.
func schemasChanged() {
	sendSchemasToAll()
	if plugins == nil {
		return
	}
	plugins.onMain(func() {
		for _, a := range plugins.apps() {
			a.invalidate()
		}
	})
}

// ownGlobs are the globs of the person's schemas, those of a single file
// first, which win over those of many, each in order.
func ownGlobs(own map[string]string) []string {
	globs := slices.DeleteFunc(slices.Sorted(maps.Keys(own)), func(g string) bool { return strings.HasPrefix(g, "!") })
	slices.SortStableFunc(globs, func(x, y string) int {
		switch {
		case fileGlob(x) && !fileGlob(y):
			return -1
		case fileGlob(y) && !fileGlob(x):
			return 1
		}
		return 0
	})
	return globs
}

// fileGlob reports whether a glob is a file's path, without wildcards.
func fileGlob(g string) bool { return !strings.ContainsAny(g, "*?[{") }

// jsonSchemaSettings are the settings vscode-json-language-server takes
// as json.schemas: the catalog's globs of JSON files, when on, each but
// the person's globs, which have theirs, as the server gives a file every
// schema that matches it; and a glob of the person's of many files but
// their files with schemas of their own. "" for a glob is no schema.
func jsonSchemaSettings(entries []schemaEntry, own map[string]string, on bool) map[string]any {
	globs := ownGlobs(own)
	var schemas []any
	if on {
		for _, e := range entries {
			if len(e.json) == 0 {
				continue
			}
			match := slices.Clip(e.json)
			for _, g := range globs {
				match = append(match, "!"+g)
			}
			schemas = append(schemas, map[string]any{"fileMatch": match, "url": e.URL})
		}
	}
	for _, g := range globs {
		if url := own[g]; url != "" {
			schemas = append(schemas, map[string]any{"fileMatch": append([]string{g}, fileExclusions(g, url, own)...), "url": url})
		}
	}
	// Each change of the settings replaces them all: validation is off
	// unless it says so.
	return map[string]any{"json": map[string]any{
		"schemas": schemas, "validate": map[string]any{"enable": true}, "format": map[string]any{"enable": true},
	}}
}

// fileExclusions are the "!" globs that keep the files the person gave
// another schema from glob, of url, a glob of many files.
func fileExclusions(glob, url string, own map[string]string) []string {
	if fileGlob(glob) {
		return nil
	}
	var out []string
	for _, g := range ownGlobs(own) {
		if fileGlob(g) && own[g] != url {
			out = append(out, "!"+g)
		}
	}
	return out
}

// yamlSchemaSettings are the settings rlsp-yaml takes: its
// initializationOptions, options, which a change of them replaces, with
// the person's globs in schemas, by URL, and schemaStore off when the
// catalog is; the server reads SchemaStore's catalog itself.
func yamlSchemaSettings(options json.RawMessage, own map[string]string, on bool) map[string]any {
	var settings map[string]any
	if len(options) > 0 {
		decodeJSON(options, &settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	schemas := map[string]any{}
	if given, ok := settings["schemas"].(map[string]any); ok {
		for url, globs := range given {
			schemas[url] = globs
		}
	}
	add := func(url string, globs ...string) {
		list, _ := schemas[url].([]any)
		if g, ok := schemas[url].(string); ok {
			list = []any{g}
		}
		for _, g := range globs {
			list = append(slices.Clip(list), g)
		}
		schemas[url] = list
	}
	// "" is no choice: the server reads SchemaStore's schema all the same.
	taken := maps.Clone(own)
	maps.DeleteFunc(taken, func(_, url string) bool { return url == "" })
	globs := ownGlobs(taken)
	for _, g := range globs {
		add(taken[g], g)
	}
	// After the globs, as the last that matches wins.
	for _, g := range globs {
		add(taken[g], fileExclusions(g, taken[g], taken)...)
	}
	settings["schemas"] = schemas
	if !on {
		settings["schemaStore"] = false
	}
	return settings
}

// schemaSends keeps the schemas a server is sent in order.
var schemaSends sync.Mutex

// sendSchemas sends the server, of c, its schemas, as its plugin says it
// takes them.
func (s *lspServer) sendSchemas(c *lsp.Client) {
	var own map[string]string
	on := true
	if plugins != nil {
		if p := plugins.prefsSnap.Load(); p != nil {
			own, on = p.FileSchemas, p.SchemaStore
		}
	}
	schemaSends.Lock()
	defer schemaSends.Unlock()
	var settings map[string]any
	switch s.contrib.Schemas {
	case "json":
		entries, _ := schemaCatalog()
		settings = jsonSchemaSettings(entries, own, on)
	case "yaml":
		settings = yamlSchemaSettings(s.launch.Options, own, on)
	default:
		return
	}
	c.Notify("workspace/didChangeConfiguration", map[string]any{"settings": settings})
}

// sendSchemasToAll sends each server running that takes schemas its own.
func sendSchemasToAll() {
	lspServers.Lock()
	var running []*lspServer
	for _, s := range lspServers.m {
		if s.contrib.Schemas != "" && s.state() == lspRunning {
			running = append(running, s)
		}
	}
	lspServers.Unlock()
	for _, s := range running {
		if c := s.live(); c != nil {
			s.sendSchemas(c)
		}
	}
}

// schemaStoreOn reports whether SchemaStore's catalog is on.
func schemaStoreOn() bool {
	if plugins != nil {
		if p := plugins.prefsSnap.Load(); p != nil {
			return p.SchemaStore
		}
	}
	return true
}
