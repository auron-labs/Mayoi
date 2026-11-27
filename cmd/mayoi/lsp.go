package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"mayoi/internal/bg"
	"mayoi/internal/lsp"
	"mayoi/internal/plugin"
	"mayoi/internal/remote"
	"mayoi/internal/settings"
	"mayoi/internal/workspace"
	"mayoi/locales"
)

// The language servers plugins declare and the person sets up in
// settings.json: started for a folder as a document they serve opens
// there, on the folder's machine, and told of the documents they serve as
// they open, change, save and close.

// lspServer is a language server of a folder.
type lspServer struct {
	plugin  *plugin.Plugin // nil for one the person set up
	contrib plugin.ServerContrib
	root    string
	key     string // of lspServers
	client  *lsp.Client
	ready   chan struct{} // closed once started, or failed to
	err     error
	failure lspFailure
	// tsc is the version of TypeScript a tsc says it is, as "7.0.2", for
	// lspFailNoTypeScript and lspFailOldTSC; "" when it could not tell.
	tsc      string
	command  string       // the program it ran, once resolved
	launch   serverLaunch // how it ran, once resolved
	failedAt time.Time    // when it could not start
	starts   int          // since its plugin was last loaded or turned on
	// asked is whether a person asked for it to start, as a restart does.
	asked bool
	// ctx is cancelled to stop it starting; stopping is set as it is
	// stopped on purpose, so its end is not told of.
	ctx      context.Context
	cancel   context.CancelFunc
	stopping atomic.Bool
}

// lspFailure is why a language server could not start.
type lspFailure int

const (
	lspFailOther   lspFailure = iota // as err says
	lspFailMissing                   // its program is not on the folder's machine
	lspFailQuit                      // its program ended before it answered
	lspFailSilent                    // it did not answer in time
	// its program ended with 127 on the folder's host, and whether it is
	// there could not be told
	lspFailUnchecked
	// typescript-language-server found no TypeScript with a tsserver in the
	// folder: tsc is the version of the folder's machine's tsc, which from
	// TypeScript 7 brings its own server
	lspFailNoTypeScript
	// its program is a tsc before TypeScript 7's, which has no server
	lspFailOldTSC
)

// lspStartTimeout is how long a server has to start and answer, and
// lspCheckTimeout how long its host has to say whether its program is
// there, when that is not clear from how it ended.
var (
	lspStartTimeout = 30 * time.Second
	lspCheckTimeout = 10 * time.Second
)

// lspState is where a language server is in its life.
type lspState int

const (
	lspStarting lspState = iota // its command starts, and it is greeted
	lspRunning                  // client talks to it
	lspFailed                   // it could not start, as err says
	lspStopped                  // it ran, and its command ended
)

// state works out the server's state from ready and its client, which
// start writes before it closes ready.
func (s *lspServer) state() lspState {
	select {
	case <-s.ready:
	default:
		return lspStarting
	}
	if s.client == nil {
		return lspFailed
	}
	select {
	case <-s.client.Done():
		return lspStopped
	default:
		return lspRunning
	}
}

var lspServers = struct {
	sync.Mutex
	m map[string]*lspServer // by folder and server id
}{m: map[string]*lspServer{}}

// serverKey is the key of the server of id in the folder root.
func serverKey(root, id string) string { return root + "\x00" + id }

// current reports whether s is still the server of its folder: one
// restarted, forgotten or stopped as the app quits is not, and what it
// would do then is left to the one after it.
func (s *lspServer) current() bool {
	lspServers.Lock()
	defer lspServers.Unlock()
	return lspServers.m[s.key] == s
}

// maxServerStarts is how many times a server is started, after it stops
// or fails to start, until its plugin is reloaded or turned on again.
const maxServerStarts = 3

// lspRetryPause is how long after a server fails to start a document of
// its language opening starts it again.
var lspRetryPause = 30 * time.Second

// docServer is a server that serves a document, and its plugin, nil for
// the person's.
type docServer struct {
	plugin  *plugin.Plugin
	contrib *plugin.ServerContrib
}

// serversFor are the servers of a document, in the order in which they
// answer what only one answers, as Go to Definition.
func (a *app) serversFor(d *document) []docServer {
	rel, _ := a.relOf(d.path)
	own, _ := settings.ValidLanguageServers(a.prefs().LanguageServers)
	return selectServers(own, plugins.enabled(), languageOf(d), rel)
}

// selectServers are the servers of a document of language lang at rel in
// the folder, with slashes, "" for one outside it. A server serves a
// document of one of its languages, or whose rel matches one of its files,
// beside every other that does: the person's first, in their order, then
// the plugins' by their files, then by their languages. A plugin's server
// the person's replaces, by its id, serves nothing. When an exclusive
// server serves a document, the servers that are not exclusive leave it to
// it, as the YAML server leaves gonsible Ansible's files.
func selectServers(own []settings.LanguageServer, enabled []*plugin.Plugin, lang, rel string) []docServer {
	var out, byLanguage []docServer
	for _, s := range own {
		byLang := lang != "" && slices.Contains(s.Languages, lang)
		byFile := rel != "" && workspace.ParseGlobs(strings.Join(s.Files, ",")).Match(rel)
		if byLang || byFile {
			out = append(out, docServer{nil, userServerContrib(s)})
		}
	}
	for _, p := range enabled {
		for i := range p.Contributes.LanguageServers {
			s := &p.Contributes.LanguageServers[i]
			switch {
			case slices.ContainsFunc(own, func(o settings.LanguageServer) bool { return o.ID == s.ID }):
			case rel != "" && len(s.Files) > 0 && serverFiles(p, s).Match(rel):
				out = append(out, docServer{p, s})
			case lang != "" && slices.Contains(s.Languages, lang):
				byLanguage = append(byLanguage, docServer{p, s})
			}
		}
	}
	out = append(out, byLanguage...)
	if slices.ContainsFunc(out, func(s docServer) bool { return s.contrib.Exclusive }) {
		out = slices.DeleteFunc(out, func(s docServer) bool { return !s.contrib.Exclusive })
	}
	return out
}

// serves reports whether s is a server of d, of a's folder.
func (a *app) serves(s *lspServer, d *document) bool {
	return slices.ContainsFunc(a.serversFor(d), func(ds docServer) bool {
		return ds.plugin == s.plugin && ds.contrib.ID == s.contrib.ID
	})
}

// serverFiles are the globs of a plugin's server's files, with its
// plugin's settings; none when a setting they take is not a string.
func serverFiles(p *plugin.Plugin, s *plugin.ServerContrib) workspace.Globs {
	settings, _ := serverSettings(p)
	files, err := expandServerArgs(s.Files, settings)
	if err != nil {
		return nil
	}
	return workspace.ParseGlobs(strings.Join(files, ","))
}

func userServerContrib(s settings.LanguageServer) *plugin.ServerContrib {
	return &plugin.ServerContrib{ID: s.ID, Languages: s.Languages, Command: s.Command, Args: s.Args, Options: s.Options,
		Exclusive: s.Exclusive}
}

// log adds a line to the log of the server's plugin, and of the person's
// servers to Mayoi's own.
func (s *lspServer) log(level, msg string) {
	if s.plugin != nil {
		s.plugin.Log(level, msg)
		return
	}
	log.Printf("mayoi: %s: %s", level, msg)
}

// serverURI is the URI a server of root knows a path by: on an SSH host,
// the path there.
func serverURI(path string) string {
	if _, rp, ok := remote.Split(path); ok {
		return lsp.URI(rp)
	}
	return lsp.URI(path)
}

// serverPath is the path of a URI of a server of root.
func serverPath(root, uri string) string {
	p := lsp.Path(uri)
	if p == "" {
		return ""
	}
	if remote.Is(root) {
		return remote.On(root, p)
	}
	return p
}

// lspFor are the servers of a document in the folder, in the order of
// serversFor, each started in the background the first time; none when
// none serves it. opening is whether the document is opening, which starts
// again one that failed to start a while ago.
func (a *app) lspFor(d *document, opening bool) []*lspServer {
	if a.root == "" || d.path == "" {
		return nil
	}
	served := a.serversFor(d)
	out := make([]*lspServer, 0, len(served))
	lspServers.Lock()
	defer lspServers.Unlock()
	for _, ds := range served {
		key := serverKey(a.root, ds.contrib.ID)
		s := lspServers.m[key]
		// One that stopped is started again, a few times, and one that
		// could not start as well, as a document opens a while after.
		if s != nil {
			st := s.state()
			if s.starts >= maxServerStarts || st == lspStarting || st == lspRunning ||
				st == lspFailed && (!opening || time.Since(s.failedAt) < lspRetryPause) {
				out = append(out, s)
				continue
			}
		}
		starts := 0
		if s != nil {
			starts = s.starts
		}
		s = newServer(ds.plugin, *ds.contrib, a.root, starts+1)
		lspServers.m[key] = s
		go s.start(a, nil)
		out = append(out, s)
	}
	return out
}

// lspRunningFor are the servers of document d that run, in the order of
// serversFor: unlike lspFor, it starts none, nor again one that stopped.
func (a *app) lspRunningFor(d *document) []*lspServer {
	if a.root == "" || d.path == "" {
		return nil
	}
	served := a.serversFor(d)
	lspServers.Lock()
	defer lspServers.Unlock()
	var out []*lspServer
	for _, ds := range served {
		if s := lspServers.m[serverKey(a.root, ds.contrib.ID)]; s != nil && s.state() == lspRunning {
			out = append(out, s)
		}
	}
	return out
}

// firstReady waits for servers in their order, for limit in all, and is
// the first that runs and can; none when none can by then.
func firstReady(servers []*lspServer, limit time.Duration, can func(*lspServer, *lsp.Client) bool) (*lspServer, *lsp.Client) {
	deadline := time.Now().Add(limit)
	for _, s := range servers {
		if c := s.wait(time.Until(deadline)); c != nil && can(s, c) {
			return s, c
		}
	}
	return nil, nil
}

// notRunning is the first of servers that does not run, which a question
// none answered is told of by; nil when all run.
func notRunning(servers []*lspServer) *lspServer {
	for _, s := range servers {
		if s.state() != lspRunning {
			return s
		}
	}
	return nil
}

// newServer is a server of root yet to start, for lspServers.
func newServer(p *plugin.Plugin, sc plugin.ServerContrib, root string, starts int) *lspServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &lspServer{plugin: p, contrib: sc, root: root, key: serverKey(root, sc.ID), ready: make(chan struct{}), starts: starts,
		ctx: ctx, cancel: cancel}
}

// start starts the server on the folder's machine, once prev, the one
// before it there, is gone.
func (s *lspServer) start(a *app, prev *lspServer) {
	defer func() {
		// One that could not start is told of once, in the window that
		// started it, unless another took its place.
		if s.client == nil {
			s.failure = s.classify()
			switch s.failure {
			case lspFailUnchecked:
				s.failure = s.check()
			case lspFailNoTypeScript:
				s.tsc = s.checkTSC("tsc")
			case lspFailQuit:
				// TypeScript 5 and 6's tsc ends at --lsp.
				if s.program() == "tsc" {
					if v := s.checkTSC(s.command); tscMajor(v) > 0 && tscMajor(v) < 7 {
						s.failure, s.tsc = lspFailOldTSC, v
					}
				}
			}
			s.failedAt = time.Now()
			if s.ctx.Err() == nil && s.current() {
				msg := s.failMessage()
				a.post(func() {
					a.fails++
					a.tell(note{message: msg, label: locales.RunningRestart(), action: func() { a.restartServer(s.key) }})
				})
			}
		}
		close(s.ready)
	}()
	if prev != nil {
		prev.stop()
		if plugins != nil {
			plugins.setDiagnosticList(s.owner(), "", "", true, nil)
		}
	}
	ctx, cancel := context.WithTimeout(s.ctx, lspStartTimeout)
	defer cancel()
	if ctx.Err() != nil {
		s.err = ctx.Err()
		return
	}
	launch, err := s.resolve(ctx)
	if err != nil {
		s.err = err
		s.log("error", "could not resolve "+s.contrib.ID+": "+err.Error())
		return
	}
	pr, pw := io.Pipe()
	cmd, err := managedCommand(ctx, s.root, launch.environment(), launch.UnsetEnv, pr, launch.Command, launch.Args...)
	if err != nil {
		pr.Close()
		pw.Close()
		s.err = err
		return
	}
	s.command, s.launch = launch.Command, launch
	owner := s.owner()
	root := s.root
	rootURI := serverURI(root)
	if len(launch.WorkspaceFolders) > 0 {
		rootURI = launch.WorkspaceFolders[0].URI
	}
	c, err := lsp.Start(ctx, lsp.Options{Cmd: cmd, Stdin: pw, RootURI: rootURI, InitOptions: launch.Options,
		Configuration: launch.Configuration, WorkspaceFolders: launch.WorkspaceFolders, PathMappings: launch.PathMappings,
		Handlers: lsp.Handlers{
			Diagnostics: func(uri string, items []lsp.Diagnostic) {
				path := serverPath(root, uri)
				if path == "" || plugins == nil || !s.current() {
					return
				}
				list := make([]diagnostic, len(items))
				for i, it := range items {
					sev := []string{"error", "error", "warning", "info", "hint"}[min(max(it.Severity, 0), 4)]
					code := strings.Trim(string(it.Code), `"`)
					list[i] = diagnostic{Range: textRange{fromLSP(it.Range.Start), fromLSP(it.Range.End)}, Severity: sev, Message: it.Message, Code: code}
				}
				source := s.contrib.ID
				if len(items) > 0 && items[0].Source != "" {
					source = items[0].Source
				}
				plugins.setDiagnosticList(owner, path, source, false, list)
			},
			Log: func(level, msg string) { s.log(level, s.contrib.ID+": "+msg) },
		}})
	if err != nil {
		pr.Close()
		pw.Close()
		s.err = fmt.Errorf("%s: %w", launch.Command, err)
		s.log("error", "could not start "+s.contrib.ID+": "+err.Error())
		return
	}
	s.client = c
	s.log("info", "started "+s.contrib.ID)
	// Before the documents open, so that they are checked with them.
	if s.contrib.Schemas != "" {
		loadSchemaCatalog()
		s.sendSchemas(c)
		refreshSchemaCatalog(schemaStoreOn(), false, nil)
	}
	// The documents open of its languages, in each window of the folder.
	a.post(func() {
		if !s.current() {
			return
		}
		for _, w := range s.windows(a) {
			for _, d := range w.docs {
				if w.serves(s, d) {
					c.Open(serverURI(d.path), languageOf(d), d.buf.String())
				}
			}
		}
		if s.asked {
			a.tell(note{message: locales.LspRestarted(s.contrib.ID)})
		}
	})
	go func() {
		<-c.Done()
		s.log("warn", s.contrib.ID+" stopped")
		// One stopped on purpose, or replaced, leaves the diagnostics to
		// what comes after it.
		if s.stopping.Load() || !s.current() {
			return
		}
		if plugins != nil {
			plugins.setDiagnosticList(owner, "", "", true, nil)
		}
		a.post(func() {
			a.tell(note{message: locales.LspStoppedOf(s.contrib.ID), label: locales.RunningRestart(),
				action: func() { a.restartServer(s.key) }})
		})
	}()
}

// owner is whose the server's diagnostics are, of its folder's.
func (s *lspServer) owner() string { return "lsp:" + s.contrib.ID + "\x00" + s.root }

// windows are the windows of the server's folder, a among them when it
// still shows it.
func (s *lspServer) windows(a *app) []*app {
	var ws []*app
	if a.root == s.root {
		ws = append(ws, a)
	}
	if plugins != nil {
		for _, w := range plugins.apps() {
			if w != a && w.root == s.root {
				ws = append(ws, w)
			}
		}
	}
	return ws
}

// stop stops the server, starting or running, and returns once its
// process is gone.
func (s *lspServer) stop() {
	s.stopping.Store(true)
	s.cancel()
	<-s.ready
	if s.client != nil {
		s.client.Close()
	}
}

// forgetFailedServers forgets the servers of the plugins of names, or of
// every plugin and user server for none, that could not start or stopped,
// so that opening a document they serve starts them afresh.
func forgetFailedServers(names ...string) {
	lspServers.Lock()
	defer lspServers.Unlock()
	for key, s := range lspServers.m {
		if st := s.state(); (st == lspFailed || st == lspStopped) && (len(names) == 0 || s.plugin != nil && slices.Contains(names, s.plugin.Name)) {
			delete(lspServers.m, key)
		}
	}
}

// startServers starts the servers of the documents open, those that
// failed forgotten.
func (h *pluginHost) startServers() {
	for _, a := range h.apps() {
		for _, d := range a.docs {
			a.lspFor(d, true)
		}
	}
}

// fromLSP is a position of LSP's as plugins have it, the same.
func fromLSP(p lsp.Position) position { return position(p) }

// client is the server's client once it runs, nil before or after.
func (s *lspServer) live() *lsp.Client {
	if s == nil || s.state() != lspRunning {
		return nil
	}
	return s.client
}

// wait waits for the server to start, for so long.
func (s *lspServer) wait(limit time.Duration) *lsp.Client {
	if s == nil {
		return nil
	}
	select {
	case <-s.ready:
	case <-time.After(limit):
	}
	return s.live()
}

// lspDoc tells a document's server what happened to it: "open", "change",
// "save" or "close".
func (a *app) lspDoc(event string, d *document) {
	if event == "close" {
		lspServers.Lock()
		servers := make([]*lspServer, 0, len(lspServers.m))
		for _, s := range lspServers.m {
			servers = append(servers, s)
		}
		lspServers.Unlock()
		for _, s := range servers {
			if c := s.live(); c != nil {
				c.Closed(serverURI(d.path))
			}
		}
		return
	}
	servers := a.lspFor(d, event == "open")
	if len(servers) == 0 && event == "open" {
		a.offerServer(d)
	}
	uri := serverURI(d.path)
	var text *string
	for _, s := range servers {
		c := s.live()
		if c == nil {
			continue // the server opens it as it starts
		}
		switch event {
		case "save":
			c.Saved(uri)
		default:
			if text == nil {
				t := d.buf.String()
				text = &t
			}
			c.Open(uri, languageOf(d), *text)
		}
	}
}

// stopServers stops every language server, as the app quits.
func stopServers() { closeServers(takeServers()) }

// takeServers forgets every language server, and returns them.
func takeServers() []*lspServer {
	lspServers.Lock()
	defer lspServers.Unlock()
	servers := make([]*lspServer, 0, len(lspServers.m))
	for _, s := range lspServers.m {
		servers = append(servers, s)
	}
	lspServers.m = map[string]*lspServer{}
	return servers
}

// closeServers closes the servers that run.
func closeServers(servers []*lspServer) {
	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.stop()
		}()
	}
	wg.Wait()
}

// serversFailed says what of the person's language servers in settings.json
// Mayoi could not use.
func (a *app) serversFailed(list []settings.LanguageServer) {
	if _, problems := settings.ValidLanguageServers(list); len(problems) > 0 {
		a.fail("settings.json: %s", strings.Join(problems, "; "))
	}
}

// restartServers stops every language server, as the person's own
// changed, and starts those the documents open need, as they now choose.
func (h *pluginHost) restartServers() {
	servers := takeServers()
	go func() {
		closeServers(servers)
		h.onMain(h.startServers)
	}()
}

// caretPosition is where the caret of an editor is, as LSP has it.
func caretPosition(d *editor) position {
	snap := d.buf.Snapshot()
	return positionOf(snap, min(d.sel[1], snap.Len()))
}

// showHover asks the server and the plugins of the document's language
// what is at the caret, and shows it. Asked again while its card shows,
// it gives the card the keys that scroll it.
func (a *app) showHover() {
	d := a.textEditor()
	if d == nil {
		return
	}
	if h := a.hover; h != nil && h.ed == d && !a.staleHover(h) {
		// The pointer's card, given the keys, stays as the pointer leaves.
		h.auto, h.focused = false, true
		return
	}
	// From the menu too, the pointer still on a name asks nothing more.
	a.hushRest()
	snap := d.buf.Snapshot()
	a.askHover(d, min(d.sel[1], snap.Len()), nil)
}

// askHover asks the servers and the plugins of d's language what is at
// rune at, and shows what each says, in the servers' order. rest is the
// name the pointer rests on, asked of quietly: of servers that run, saying
// nothing of none or a failure; nil is the caret, as Show Hover asks.
func (a *app) askHover(d *editor, at int, rest *pointerHover) {
	pos := positionOf(d.buf.Snapshot(), at)
	var servers []*lspServer
	if rest == nil {
		servers = a.lspFor(d.document, false)
	} else {
		servers = a.lspRunningFor(d.document)
	}
	doc := a.docJSON(d.document)
	lang := languageOf(d.document)
	uri := serverURI(d.path)
	var providers []*plugin.Plugin
	if plugins != nil {
		for _, p := range plugins.enabled() {
			if slices.Contains(p.HoverLanguages(), lang) {
				providers = append(providers, p)
			}
		}
	}
	if rest != nil && len(servers) == 0 && len(providers) == 0 {
		return
	}
	text := d.buf.String()
	var current func() bool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if rest == nil {
		current = a.lspAsked(servers, d, &a.hoverAsked)
	} else {
		// Of the editor the pointer is over, which need not have the
		// keyboard: the name it still rests on, its text as it was.
		a.restAsked++
		n, version, word, picks := a.restAsked, d.buf.Version(), rest.word, a.picksOpened
		rest.ctx, rest.cancel = ctx, cancel
		current = func() bool {
			r := &a.rest
			return a.restAsked == n && r.ed == d && r.word == word && d.buf.Version() == version &&
				a.picksOpened == picks && allCurrent(servers)
		}
	}
	auto := rest != nil
	bg.Go(func() {
		defer cancel()
		answers := make([]lsp.Hover, len(servers))
		var wg sync.WaitGroup
		for i, s := range servers {
			wg.Go(func() {
				if c := s.wait(3 * time.Second); c != nil && c.Hover() {
					// The server knows the text as it is now.
					c.Open(uri, lang, text)
					if hover, err := c.HoverAt(ctx, uri, lsp.Position(pos)); err == nil {
						answers[i] = hover
					}
				}
			})
		}
		wg.Wait()
		var parts []string
		var span *lsp.Range
		for _, hover := range answers {
			if strings.TrimSpace(hover.Markdown) != "" {
				parts = append(parts, hover.Markdown)
				if span == nil {
					span = hover.Range
				}
			}
		}
		for _, p := range providers {
			if ctx.Err() != nil {
				break
			}
			res, err := p.Request(ctx, "hover", map[string]any{"language": lang, "document": doc, "position": pos})
			var text string
			if err == nil && json.Unmarshal(res, &text) == nil && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		a.post(func() {
			if !current() {
				return
			}
			if len(parts) == 0 {
				if auto {
					return // the pointer asked: nothing shows, nothing is said
				}
				// A server not running did not say there is nothing.
				if s := notRunning(servers); s != nil {
					a.lspFailed(s)
					return
				}
				a.tell(note{message: locales.HoverNone()})
				return
			}
			if h := a.hover; auto && h != nil && !h.auto && !a.staleHover(h) {
				return // the card asked for at the caret stays
			}
			h := newHoverCard(d, strings.Join(parts, "\n\n---\n\n"), span, at)
			h.auto = auto
			a.hover = h
		})
	})
}

// lspAsked counts a question asked at the caret of d, of servers, in
// asked, and is whether its answer still goes: the last of its kind asked,
// of servers still the folder's, at the caret, view and text it was asked
// at. One gone on from, a server restarted, or a picker opened since, as
// Quick Open, leaves it too late.
func (a *app) lspAsked(servers []*lspServer, d *editor, asked *int) func() bool {
	*asked++
	n, root, version, sel, picks := *asked, a.root, d.buf.Version(), d.sel, a.picksOpened
	return func() bool {
		return *asked == n && a.root == root && a.activeEditor() == d && d.buf.Version() == version && d.sel == sel &&
			a.picksOpened == picks && allCurrent(servers)
	}
}

// allCurrent reports whether each of servers is still its folder's.
func allCurrent(servers []*lspServer) bool {
	for _, s := range servers {
		if !s.current() {
			return false
		}
	}
	return true
}

const sourceDefinition lsp.Navigation = "sourceDefinition"

func (a *app) jumpCommand(kind lsp.Navigation) func() {
	return func() {
		if d := a.textEditor(); d != nil {
			a.locationsAt(d, kind, false)
		}
	}
}

// goToDefinition goes to where what is at the caret is defined, as the
// document's language server says.
func (a *app) goToDefinition() {
	if d := a.textEditor(); d != nil {
		a.definitionAt(d, false)
	}
}

// definitionAt goes to where what is at the caret of d is defined, or
// offers the places when there are several. A click, which a Cmd-click is,
// says nothing of a language with no server.
func (a *app) definitionAt(d *editor, click bool) {
	a.locationsAt(d, lsp.Definition, click)
}

// locationsAt uses the same document, stale-answer checks and targets for
// keyboard commands and Cmd-click. A click says nothing about missing servers.
// The first of the document's servers that answers kind is asked.
func (a *app) locationsAt(d *editor, kind lsp.Navigation, click bool) {
	servers := a.lspFor(d.document, false)
	current := a.lspAsked(servers, d, &a.defAsked)
	if len(servers) == 0 {
		if !click {
			a.tell(note{message: locales.LspNone(languageOf(d.document))})
		}
		return
	}
	pos, uri, root := caretPosition(d), serverURI(d.path), a.root
	lang, text := languageOf(d.document), d.buf.String()
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s, c := firstReady(servers, 5*time.Second, func(s *lspServer, c *lsp.Client) bool {
			if kind == sourceDefinition {
				return sourceDefinitionMethod(s, c) != "" || c.SupportsCommand(s.contrib.SourceDefinitionCommand)
			}
			return c.CanNavigate(kind)
		})
		if c == nil {
			a.post(func() {
				switch failed := notRunning(servers); {
				case !current():
				case failed != nil:
					a.lspFailed(failed)
				case !click:
					a.tell(note{message: locales.LspNavigationUnsupported()})
				}
			})
			return
		}
		sourceMethod, command := "", s.contrib.SourceDefinitionCommand
		if kind == sourceDefinition {
			sourceMethod = sourceDefinitionMethod(s, c)
		}
		// The server knows the text as it is now: not as the last change
		// it was told of a moment ago, nor before the documents open are
		// told of, as a server that just started is.
		c.Open(uri, lang, text)
		var locs []lsp.Location
		var err error
		if sourceMethod != "" {
			locs, err = c.SourceDefinitionRequestAt(ctx, sourceMethod, uri, lsp.Position(pos))
		} else if kind == sourceDefinition {
			locs, err = c.SourceDefinitionAt(ctx, command, uri, lsp.Position(pos))
		} else {
			locs, err = c.NavigateAt(ctx, kind, uri, lsp.Position(pos))
		}
		a.post(func() {
			if !current() {
				return
			}
			// A server that stopped or restarted did not say there is none.
			if err != nil && s.state() != lspRunning {
				a.lspFailed(s)
				return
			}
			if err != nil {
				s.log("warn", fmt.Sprintf("%s: %v", kind, err))
				a.tell(note{message: locales.LspNavigationFailed()})
				return
			}
			if len(locs) == 0 {
				message := locales.LspNavigationNone()
				if kind == lsp.Definition {
					message = locales.DefinitionNone()
				}
				a.tell(note{message: message})
				return
			}
			type place struct {
				path string
				loc  lsp.Location
			}
			type spot struct {
				path string
				pos  lsp.Position
			}
			places := make([]place, 0, len(locs))
			seen := make(map[spot]bool, len(locs))
			lines := make(map[spot]int)
			for _, l := range locs {
				path := serverPath(root, l.URI)
				target := spot{path: path, pos: l.Range.Start}
				if path == "" || seen[target] {
					continue
				}
				seen[target] = true
				lines[spot{path: path, pos: lsp.Position{Line: target.pos.Line}}]++
				places = append(places, place{path, l})
			}
			if len(places) == 0 {
				message := locales.LspLocationElsewhere(locs[0].URI)
				if kind == lsp.Definition {
					message = locales.DefinitionElsewhere(locs[0].URI)
				}
				a.tell(note{message: message})
				return
			}
			if len(places) == 1 && kind != lsp.References {
				a.goToLocation(places[0].path, places[0].loc)
				return
			}
			items := make([]pickItem, len(places))
			for i, p := range places {
				start := p.loc.Range.Start
				label := filepath.Base(p.path) + ":" + strconv.Itoa(start.Line+1)
				// Places on one line are told apart by their column.
				if kind == lsp.References || lines[spot{path: p.path, pos: lsp.Position{Line: start.Line}}] > 1 {
					label += ":" + strconv.Itoa(start.Character+1)
				}
				items[i] = pickItem{Label: label, Description: a.relPath(filepath.Dir(p.path)), Icon: "file"}
			}
			title := locales.LspChooseLocation()
			switch kind {
			case lsp.Definition:
				title = locales.DefinitionPick()
			case lsp.References:
				title = locales.CmdGoReferences()
			}
			a.openPluginPicker(items, title, func(i int) {
				// The list opened is a picker, which current() counts:
				// the choice goes while the folder is the same.
				if i >= 0 && a.root == root {
					a.goToLocation(places[i].path, places[i].loc)
				}
			})
			a.pluginPick.icon = iconFileSearch
		})
	})
}

// sourceDefinitionMethod is the request s asks Go to Source Definition
// with, as its plugin's resolver said, when its client c says it answers
// it; "" for none.
func sourceDefinitionMethod(s *lspServer, c *lsp.Client) string {
	if source := s.launch.SourceDefinition; source != nil && c.SupportsExperimental(source.Capability) {
		return source.Method
	}
	return ""
}

// goToLocation shows a place a language server named, the caret at its
// start.
func (a *app) goToLocation(path string, loc lsp.Location) {
	a.openFileAt(path, loc.Range.Start.Line, true)
	if ed := a.activeEditor(); ed != nil && ed.path == path {
		// At the name, once the file is read, not the line's start.
		ed.whenRead(func(ed *editor) {
			at := posRune(ed.buf.Snapshot(), fromLSP(loc.Range.Start))
			ed.selectRange(at, at, true)
			ed.lightLanding()
		})
	}
}

// lspFailed tells why a language server did not answer: one that could
// not start was told of as it failed.
func (a *app) lspFailed(s *lspServer) {
	switch {
	case s != nil && s.state() == lspFailed:
		a.tell(note{message: s.failMessage(), label: locales.RunningRestart(), action: func() { a.restartServer(s.key) }})
		return
	case s != nil && s.state() == lspStopped:
		a.tell(note{message: locales.LspDown(), label: locales.RunningRestart(), action: func() { a.restartServer(s.key) }})
		return
	}
	a.tell(note{message: locales.LspNotReady()})
}

// complete offers the completions of the document's language server at
// the caret, in a box, and puts the one chosen in place of the word before
// the caret.
func (a *app) complete() {
	d := a.textEditor()
	if d == nil {
		return
	}
	// In a wiki link of a note, the notes it may link to.
	if a.wikiSuggestNow(d) {
		return
	}
	servers := a.lspFor(d.document, false)
	if len(servers) == 0 {
		a.tell(note{message: locales.LspNone(languageOf(d.document))})
		return
	}
	pos, uri := caretPosition(d), serverURI(d.path)
	lang, text := languageOf(d.document), d.buf.String()
	current := a.lspAsked(servers, d, &a.completeAsked)
	bg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Each server's, in their order, each sorted as it says: one's
		// sort texts mean nothing to another's.
		lists := make([][]lsp.CompletionItem, len(servers))
		errs := make([]error, len(servers))
		asked := make([]bool, len(servers))
		var wg sync.WaitGroup
		for i, s := range servers {
			wg.Go(func() {
				c := s.wait(5 * time.Second)
				if c == nil || !c.Completion() {
					return
				}
				asked[i] = true
				// The server knows the text as it is now.
				c.Open(uri, lang, text)
				lists[i], errs[i] = c.CompletionAt(ctx, uri, lsp.Position(pos))
				slices.SortStableFunc(lists[i], func(x, y lsp.CompletionItem) int {
					return strings.Compare(sortKey(x), sortKey(y))
				})
			})
		}
		wg.Wait()
		var items []lsp.CompletionItem
		var stopped *lspServer
		for i, s := range servers {
			items = append(items, lists[i]...)
			// A server that stopped did not say there is none.
			if errs[i] != nil && stopped == nil && s.state() != lspRunning {
				stopped = s
			}
		}
		a.post(func() {
			if !current() {
				return
			}
			if !slices.Contains(asked, true) {
				a.lspFailed(notRunning(servers))
				return
			}
			if len(items) == 0 && stopped != nil {
				a.lspFailed(stopped)
				return
			}
			if len(items) == 0 {
				a.tell(note{message: locales.CompletionNone()})
				return
			}
			if len(items) > 500 {
				items = items[:500]
			}
			picks := make([]pickItem, len(items))
			for i, it := range items {
				picks[i] = pickItem{Label: it.Label, Description: it.Detail, Icon: completionIcon(it.Kind)}
			}
			a.openPluginPicker(picks, locales.CompletionPlaceholder(), func(i int) {
				if i >= 0 {
					a.queueEdit(d, func(d *editor) { applyCompletion(d, items[i]) })
				}
			})
		})
	})
}

func sortKey(it lsp.CompletionItem) string {
	if it.SortText != "" {
		return it.SortText
	}
	return it.Label
}

// applyCompletion puts a completion in place: its edit, or its text in
// place of the word before the caret.
func applyCompletion(d *editor, it lsp.CompletionItem) {
	snap := d.buf.Snapshot()
	text := it.InsertText
	if text == "" {
		text = it.Label
	}
	var s, e int
	if it.TextEdit != nil {
		s, e = posRune(snap, fromLSP(it.TextEdit.Range.Start)), posRune(snap, fromLSP(it.TextEdit.Range.End))
		text = it.TextEdit.NewText
	} else {
		e = min(d.sel[1], snap.Len())
		s = e
		for s > 0 {
			r := []rune(snap.Slice(s-1, s))
			if len(r) == 0 || !(unicode.IsLetter(r[0]) || unicode.IsDigit(r[0]) || r[0] == '_') {
				break
			}
			s--
		}
	}
	if it.InsertTextFormat == 2 {
		d.selectRange(s, e, false)
		d.sel = [2]int{s, e}
		expandSnippet(d, text, map[string]string{})
		return
	}
	at := s + len([]rune(text))
	d.edit([2]int{at, at}, textEdit{s, e, text})
}

// completionIcon is the icon of a kind of completion.
func completionIcon(kind int) string {
	switch kind {
	case 2, 3, 4: // method, function, constructor
		return "function"
	case 5, 6, 10: // field, variable, property
		return "hash"
	case 7, 8, 22: // class, interface, struct
		return "box"
	case 9: // module
		return "package"
	case 14: // keyword
		return "keyboard"
	case 15: // snippet
		return "braces"
	case 17: // file
		return "file"
	case 19: // folder
		return "folder"
	}
	return "code"
}

// lspFormat formats a document with the first of its language servers
// that runs and formats, within limit; it reports false when there is
// none.
func (a *app) lspFormat(d *editor, limit time.Duration, then func()) bool {
	var c *lsp.Client
	for _, s := range a.lspFor(d.document, false) {
		if live := s.live(); live != nil && live.Formatting() {
			c = live
			break
		}
	}
	if c == nil {
		return false
	}
	c.Open(serverURI(d.path), languageOf(d.document), d.buf.String())
	snap := d.buf.Snapshot()
	version := snap.Version()
	size, spaces := max(d.indent.size, 1), !d.indent.tabs
	if d.indent.tabs {
		size = tabWidth()
	}
	uri := serverURI(d.path)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		edits, err := c.Format(ctx, uri, size, spaces)
		a.post(func() {
			defer func() {
				if then != nil {
					then()
				}
			}()
			if err != nil {
				if !errors.Is(err, context.DeadlineExceeded) {
					a.fail("%s", locales.LspFailed(firstLine(err.Error())))
				}
				return
			}
			if d.buf.Version() != version || len(edits) == 0 {
				return
			}
			data, _ := json.Marshal(lspEdits(edits))
			a.applyFormat(d, snap, data)
		})
	}()
	return true
}

func lspEdits(edits []lsp.TextEdit) []map[string]any {
	out := make([]map[string]any, len(edits))
	for i, e := range edits {
		out[i] = map[string]any{"range": e.Range, "text": e.NewText}
	}
	return out
}
