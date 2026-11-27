package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A server's program is found in the folder of the servers Mayoi
// installed before the path, and nothing but a server's start has it
// searched; a gem's finds its gems.
func TestManagedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("programs are marked executable by their extension")
	}
	data := t.TempDir()
	t.Setenv("MAYOI_DATA_DIR", data)
	dir := filepath.Join(data, "servers")
	for _, bin := range []string{"bin", "gems/bin"} {
		os.MkdirAll(filepath.Join(dir, bin), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "bin", "mayoi-a-ls"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "gems", "bin", "mayoi-b-ls"), []byte("#!/bin/sh\n"), 0o755)

	cmd, err := managedCommand(context.Background(), t.TempDir(), []string{"A=1"}, nil, nil, "mayoi-a-ls", "--stdio")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != filepath.Join(dir, "bin", "mayoi-a-ls") || !slices.Contains(cmd.Env, "A=1") {
		t.Fatalf("started %s with %q", cmd.Path, cmd.Env)
	}
	if path := os.Getenv("PATH"); !slices.Contains(cmd.Env, "PATH="+filepath.Join(dir, "bin")+":"+filepath.Join(dir, "gems", "bin")+":"+dir+":"+path) {
		t.Fatalf("the path: %q", cmd.Env)
	}
	if slices.ContainsFunc(cmd.Env, func(v string) bool { return strings.HasPrefix(v, "GEM_HOME=") }) {
		t.Fatal("gems set for a program that is not a gem's")
	}

	cmd, _ = managedCommand(context.Background(), t.TempDir(), nil, nil, nil, "mayoi-b-ls")
	if !slices.Contains(cmd.Env, "GEM_HOME="+filepath.Join(dir, "gems")) || !slices.Contains(cmd.Env, "GEM_PATH="+filepath.Join(dir, "gems")) {
		t.Fatalf("a gem's program without its gems: %q", cmd.Env)
	}

	if found, known := programOnHost(context.Background(), t.TempDir(), nil, nil, "mayoi-a-ls"); !found || !known {
		t.Fatal("the program in the managed folder is not found")
	}
}
