package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Renaming goes through the path mappings: the server is asked of a
// document by its own path, and the files it edits come back by the
// client's, those of the folder not open among them.
func TestRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "other.fake"), []byte("see foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MAYOI_FAKE_LSP=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cmd: cmd, RootURI: URI(root),
		PathMappings: []PathMapping{{ClientURI: "file:///work", ServerURI: URI(root)}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.CanRename() || !c.CanPrepareRename() {
		t.Fatal("the rename capabilities were not read")
	}
	uri := "file:///work/a.fake"
	c.Open(uri, "fake", "foo bar foo\nnothing\n")

	target, err := c.PrepareRename(ctx, uri, Position{0, 9})
	if err != nil || target == nil || target.Placeholder != "foo" || *target.Range != (Range{Position{0, 8}, Position{0, 11}}) {
		t.Fatalf("prepare = %+v, %v", target, err)
	}
	if target, err = c.PrepareRename(ctx, uri, Position{1, 2}); err != nil || target != nil {
		t.Fatalf("prepare of nothing = %+v, %v", target, err)
	}

	raw, err := c.Rename(ctx, uri, Position{0, 1}, "baz")
	if err != nil {
		t.Fatal(err)
	}
	files, err := ParseEdit(raw)
	if err != nil || len(files) != 2 {
		t.Fatalf("edit = %s, %v", raw, err)
	}
	for _, f := range files {
		switch f.URI {
		case uri:
			if len(f.Edits) != 2 || f.Edits[0].NewText != "baz" {
				t.Errorf("open document edits = %+v", f.Edits)
			}
		case "file:///work/other.fake":
			if len(f.Edits) != 1 || f.Edits[0].Range.Start != (Position{0, 4}) {
				t.Errorf("other file edits = %+v", f.Edits)
			}
		default:
			t.Errorf("edit of %s, which the client does not know", f.URI)
		}
	}
}
