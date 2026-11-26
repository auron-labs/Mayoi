package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// testCatalog is a catalog of SchemaStore's, of a few schemas.
const testCatalog = `{"schemas": [
	{"name": "package.json", "description": "npm's", "fileMatch": ["package.json"], "url": "https://json.schemastore.org/package.json"},
	{"name": "GitHub Workflow", "description": "Actions", "fileMatch": ["**/.github/workflows/*.yml", "**/.github/workflows/*.yaml"],
		"url": "https://json.schemastore.org/github-workflow.json"},
	{"name": "Settings", "description": "Both", "fileMatch": ["settings.json", "settings.yaml", ".settingsrc"], "url": "https://example.com/settings.json"},
	{"name": "No files", "url": "https://example.com/none.json"},
	{"description": "No name", "fileMatch": ["x.json"], "url": "https://example.com/x.json"}
]}`

// withSchemaCatalog has the tests start from no catalog read, kept at
// file, "" for nowhere, and downloaded by fetch.
func withSchemaCatalog(t testing.TB, file string, fetch func(context.Context) ([]byte, error)) {
	t.Helper()
	reset := func() {
		s := &schemaStore
		s.Lock()
		s.entries, s.byURL, s.read, s.at, s.err, s.failedAt, s.busy, s.arrived = nil, nil, false, time.Time{}, nil, time.Time{}, false, nil
		s.gen++
		s.Unlock()
	}
	oldFile, oldFetch := schemaCatalogFile, fetchSchemaCatalog
	schemaCatalogFile = func() string { return file }
	fetchSchemaCatalog = fetch
	reset()
	t.Cleanup(func() {
		schemaCatalogFile, fetchSchemaCatalog = oldFile, oldFetch
		reset()
	})
}

func offlineCatalog(context.Context) ([]byte, error) { return nil, errors.New("offline") }

// The JSON server is given the catalog's globs of JSON files and the
// person's, theirs winning as the catalog's leave their files out, as
// the server gives a file every schema that matches it; the YAML server
// keeps its own settings, takes the person's by URL, and reads the
// catalog itself unless it is off.
func TestSchemaSettings(t *testing.T) {
	entries, err := parseSchemaCatalog([]byte(testCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want those with a name and a URL", len(entries))
	}
	own := map[string]string{"config/app.json": "https://example.com/app.json", "off.json": ""}
	got, _ := json.Marshal(jsonSchemaSettings(entries, own, true))
	want := `{"json":{"format":{"enable":true},"schemas":[` +
		`{"fileMatch":["package.json","!config/app.json","!off.json"],"url":"https://json.schemastore.org/package.json"},` +
		`{"fileMatch":["settings.json",".settingsrc","!config/app.json","!off.json"],"url":"https://example.com/settings.json"},` +
		`{"fileMatch":["config/app.json"],"url":"https://example.com/app.json"}],"validate":{"enable":true}}}`
	if string(got) != want {
		t.Fatalf("json settings:\n%s\nwant\n%s", got, want)
	}
	got, _ = json.Marshal(jsonSchemaSettings(entries, own, false))
	if want := `{"json":{"format":{"enable":true},"schemas":[{"fileMatch":["config/app.json"],"url":"https://example.com/app.json"}],"validate":{"enable":true}}}`; string(got) != want {
		t.Fatalf("json settings with the catalog off: %s", got)
	}

	// A file's own schema wins over a glob of many of the person's.
	own = map[string]string{"config/*.json": "https://example.com/x.json", "config/app.json": "", "config/b.json": "https://example.com/x.json"}
	got, _ = json.Marshal(jsonSchemaSettings(nil, own, false)["json"].(map[string]any)["schemas"])
	if want := `[{"fileMatch":["config/b.json"],"url":"https://example.com/x.json"},` +
		`{"fileMatch":["config/*.json","!config/app.json"],"url":"https://example.com/x.json"}]`; string(got) != want {
		t.Fatalf("json schemas of a file's own and a glob's: %s", got)
	}
	own = map[string]string{"deploy/*.yaml": "https://example.com/x.json", "deploy/app.yaml": "https://example.com/y.json", "deploy/none.yaml": ""}
	got, _ = json.Marshal(yamlSchemaSettings(nil, own, true))
	if want := `{"schemas":{"https://example.com/x.json":["deploy/*.yaml","!deploy/app.yaml"],"https://example.com/y.json":["deploy/app.yaml"]}}`; string(got) != want {
		t.Fatalf("yaml schemas of a file's own and a glob's: %s", got)
	}

	options := json.RawMessage(`{"httpProxy":"http://proxy:3128","schemas":{"https://example.com/app.json":"deploy/*.yaml"}}`)
	own = map[string]string{"a.yaml": "https://example.com/app.json", "b.yaml": "https://example.com/b.json", "c.yaml": ""}
	got, _ = json.Marshal(yamlSchemaSettings(options, own, true))
	if want := `{"httpProxy":"http://proxy:3128","schemas":{"https://example.com/app.json":["deploy/*.yaml","a.yaml"],"https://example.com/b.json":["b.yaml"]}}`; string(got) != want {
		t.Fatalf("yaml settings: %s", got)
	}
	got, _ = json.Marshal(yamlSchemaSettings(nil, nil, false))
	if want := `{"schemaStore":false,"schemas":{}}`; string(got) != want {
		t.Fatalf("yaml settings with the catalog off: %s", got)
	}

}

// The catalog downloads in the background as a server that takes schemas
// starts, is kept, and is used as it is for a day without asking again,
// offline too; one that failed to download is not asked for again for a
// while, but at Try Again.
func TestSchemaCatalogDownloads(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cache", "catalog.json")
	var asked atomic.Int32
	fail := atomic.Bool{}
	fail.Store(true)
	withSchemaCatalog(t, file, func(context.Context) ([]byte, error) {
		asked.Add(1)
		if fail.Load() {
			return nil, errors.New("no network")
		}
		return []byte(testCatalog), nil
	})
	refresh := func(retry bool) {
		done := make(chan struct{})
		refreshSchemaCatalog(true, retry, func() { close(done) })
		<-done
	}
	refresh(false)
	if _, err := schemaCatalogState(); err == nil || err.Error() != "no network" || asked.Load() != 1 {
		t.Fatalf("err = %v, asked %d", err, asked.Load())
	}
	refresh(false)
	if asked.Load() != 1 {
		t.Fatalf("asked again %d times so soon after it failed", asked.Load())
	}
	fail.Store(false)
	refresh(true)
	if entries, _ := schemaCatalog(); len(entries) != 4 || asked.Load() != 2 {
		t.Fatalf("entries %d, asked %d", len(entries), asked.Load())
	}
	if _, err := schemaCatalogState(); err != nil {
		t.Fatalf("err = %v after it downloaded", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != testCatalog {
		t.Fatalf("kept %q, %v", data, err)
	}
	// The next run reads it, and asks for none while it is a day old.
	withSchemaCatalog(t, file, offlineCatalog)
	refresh(false)
	if entries, _ := schemaCatalog(); len(entries) != 4 {
		t.Fatalf("entries %d read from the file", len(entries))
	}
	if _, err := schemaCatalogState(); err != nil {
		t.Fatalf("asked for it: %v", err)
	}
	// One older is asked for, and kept while offline.
	old := time.Now().Add(-schemaCatalogAge - time.Minute)
	os.Chtimes(file, old, old)
	withSchemaCatalog(t, file, offlineCatalog)
	refresh(false)
	if entries, _ := schemaCatalog(); len(entries) != 4 {
		t.Fatalf("entries %d of the day-old file, offline", len(entries))
	}
	if _, err := schemaCatalogState(); err == nil {
		t.Fatal("a day-old catalog was not asked for again")
	}
}
