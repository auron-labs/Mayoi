package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"mayoi/internal/plugin"
)

const okProgram = "#!/bin/sh\necho ok\n"

func gz(data []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

func tarGz(path, data string) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	w.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data))})
	w.Write([]byte(data))
	w.Close()
	return gz(b.Bytes())
}

// A server's release, an archive or a binary, downloads into the managed
// folder with its program linked into bin and ready to run, as an
// installed program is found.
func TestDownloadScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a POSIX shell script")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ls-linux.tar.gz":
			w.Write(tarGz("ls-1.0/bin/ls-real", okProgram))
		case "/ls-linux.gz":
			w.Write(gz([]byte(okProgram)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	for _, c := range []struct{ name, file, bin string }{
		{"archive", "ls-linux.tar.gz", "ls-1.0/bin/ls-real"},
		{"gzip", "ls-linux.gz", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			out, err := exec.Command("/bin/sh", "-c", downloadScript, "sh", dir, "ls-plugin", srv.URL+"/"+c.file, c.bin, "ls-prog").CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, err := exec.Command(filepath.Join(dir, "bin", "ls-prog")).Output()
			if err != nil || string(got) != "ok\n" {
				t.Fatalf("the program linked in bin: %q, %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "ls-plugin.part")); err == nil {
				t.Fatal("the download left its folder behind")
			}
		})
	}

	// A download that fails links nothing.
	dir := t.TempDir()
	out, err := exec.Command("/bin/sh", "-c", downloadScript, "sh", dir, "ls-plugin", srv.URL+"/missing.gz", "", "ls-prog").CombinedOutput()
	if err == nil {
		t.Fatalf("a missing download installed: %s", out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "bin", "ls-prog")); err == nil {
		t.Fatal("linked a program that was not downloaded")
	}
}

// A machine the download has no asset for installs with the install
// command.
func TestInstallCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("downloads are for POSIX machines")
	}
	ctx, root := context.Background(), t.TempDir()
	system, arch, err := hostPlatform(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	dl := &plugin.ServerDownload{URL: "https://example.com/{os}-{arch}.gz", OS: map[string]string{system: system}, Arch: map[string]string{arch: "cpu"}}
	_, args, err := installCommand(ctx, root, "/m", "ls", &plugin.ServerContrib{Download: dl, Install: "make"}, "ls")
	if err != nil || !slices.Contains(args, "https://example.com/"+system+"-cpu.gz") {
		t.Fatalf("download: %q, %v", args, err)
	}
	dl.OS = map[string]string{"plan9": "plan9"}
	_, args, err = installCommand(ctx, root, "/m", "ls", &plugin.ServerContrib{Download: dl, Install: "make"}, "ls")
	if err != nil || args[1] != "make" {
		t.Fatalf("install: %q, %v", args, err)
	}
	if _, _, err = installCommand(ctx, root, "/m", "ls", &plugin.ServerContrib{Download: dl}, "ls"); err == nil {
		t.Fatal("nothing to install with, and no error")
	}
}

// zigInstalling is a bundled plugin, off, whose server's install command
// is install.
func zigInstalling(install string) string {
	return fmt.Sprintf(`{"name": "zig-ls", "displayName": "Zig", "enabled": false, "contributes": {
  "settings": {"path": {"type": "string", "default": "mayoi-zigx-ls.exe"}},
  "languageServers": [{"id": "zig-ls.server", "languages": ["zigx"], "command": "${settings.path}", "install": %q}]}}`, install)
}

// The note of a server's missing program has Install, which installs it
// into the managed folder and turns the plugin on; one that fails says
// why, logs what the install wrote, and can be tried again.
func TestInstallServerFromNote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a POSIX shell script")
	}
	t.Setenv("MAYOI_DATA_DIR", t.TempDir())
	oh := newOfferHarness(t, func() ([]plugin.IndexEntry, error) { return nil, nil })
	script := `if [ -f "$GOBIN/ok" ]; then printf '#!/bin/sh\n' > "$GOBIN/mayoi-zigx-ls.exe"; chmod +x "$GOBIN/mayoi-zigx-ls.exe"; else echo fetching >&2; echo no route to host >&2; exit 1; fi`
	plugins.reg.Dirs.Bundled = fstest.MapFS{"zig-ls/plugin.json": {Data: []byte(zigInstalling(script))}}
	plugins.reg.Load()
	plugins.setPrefs = func(key string, raw json.RawMessage) {
		oh.a.settings.Prefs.Set(key, raw)
		plugins.syncPrefs()
		plugins.reg.Load()
	}
	oh.open("a.zigx")
	oh.until("the missing server told", func() bool { return oh.tt.HasText("which isn't installed") && oh.tt.HasText("Install") })

	p, sc := offServer("zigx")
	oh.a.installServerProgram(p, sc, "mayoi-zigx-ls")
	oh.until("the failure", func() bool { return oh.tt.HasText("Could not install Zig: no route to host") })
	if !oh.tt.HasText("Try Again") || slices.Contains(plugins.enabled(), p) {
		t.Fatalf("failed: %q", oh.tt.Texts())
	}
	logged := false
	for _, l := range p.Logs() {
		logged = logged || strings.Contains(l.Message, "fetching")
	}
	if !logged {
		t.Fatal("the output of the install is not in the plugin's log")
	}

	dir, _ := serversDir(context.Background(), oh.a.root)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "ok"), nil, 0o644)
	oh.a.installServerProgram(p, sc, "mayoi-zigx-ls")
	oh.until("the plugin on", func() bool { return slices.Contains(plugins.enabled(), p) })
	if found, _ := serverInstalled(context.Background(), oh.a.root, "mayoi-zigx-ls.exe"); !found {
		t.Fatal("the installed program is not found")
	}
}
