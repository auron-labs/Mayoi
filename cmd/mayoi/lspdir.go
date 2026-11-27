package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"mayoi/internal/plugin"
	"mayoi/internal/remote"
	"mayoi/internal/vfs"
)

// The servers Mayoi installs live in a folder of the folder's machine,
// ~/.mayoi/servers, whose bin is searched before the path for the
// programs of servers: only as servers start and are looked for, so
// terminals, Git and other commands are left as they are.

// hostDirs caches the managed folder of each remote host.
var hostDirs = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// serversDir is the folder of the servers Mayoi installed on the machine
// of root: on this one, beside the user's plugins, so that MAYOI_DATA_DIR
// moves it too; on another, in its $HOME.
func serversDir(ctx context.Context, root string) (string, error) {
	if !remote.Is(root) {
		return plugin.UserDirs().Servers, nil
	}
	hostDirs.Lock()
	defer hostDirs.Unlock()
	host := remote.HostOf(root)
	if dir, ok := hostDirs.m[host]; ok {
		return dir, nil
	}
	cmd, err := vfs.Command(root, nil, nil, "/bin/sh", "-c", `printf %s "$HOME/.mayoi/servers"`)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := waitContext(ctx, cmd); err != nil {
		return "", err
	}
	hostDirs.m[host] = out.String()
	return out.String(), nil
}

// hostJoin joins paths as the machine of root does.
func hostJoin(root string) func(...string) string {
	if remote.Is(root) {
		return path.Join
	}
	return filepath.Join
}

// serverBins are the folders of dir, the managed folder of root's
// machine, that hold programs, searched in this order: bin is where the
// installers put them, gems/bin those of gem, and dir itself where npm
// puts them on Windows.
func serverBins(root, dir string) []string {
	join := hostJoin(root)
	return []string{join(dir, "bin"), join(dir, "gems", "bin"), dir}
}

// serverEnv is the environment that has the common installers put what
// they install into dir, without administrator rights.
func serverEnv(root, dir string) []string {
	join := hostJoin(root)
	return []string{
		"NPM_CONFIG_PREFIX=" + dir,
		"GOBIN=" + join(dir, "bin"),
		"UV_TOOL_BIN_DIR=" + join(dir, "bin"),
		"UV_TOOL_DIR=" + join(dir, "uv"),
		"CARGO_INSTALL_ROOT=" + dir,
		"PIPX_BIN_DIR=" + join(dir, "bin"),
		"PIPX_HOME=" + join(dir, "pipx"),
		"GEM_HOME=" + join(dir, "gems"),
	}
}

// findManaged is the program name in one of bins, on this machine, "" for
// none or for a name with a folder.
func findManaged(bins []string, name string) string {
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	for _, bin := range bins {
		if found, err := exec.LookPath(filepath.Join(bin, name)); err == nil {
			return found
		}
	}
	return ""
}

// managedCommand is vfs.CommandEnvironment for the program of a server,
// found in the managed folder before the path. One a gem installed there
// finds its gems.
func managedCommand(ctx context.Context, root string, env, unset []string, stdin io.Reader, name string, args ...string) (*exec.Cmd, error) {
	dir, err := serversDir(ctx, root)
	if err != nil {
		return vfs.CommandEnvironment(root, env, unset, stdin, name, args...)
	}
	bins := serverBins(root, dir)
	if remote.Is(root) {
		// The path's own entries come after the folder's, whatever env sets.
		const script = `PATH="$1:$PATH"; export PATH
gems=$2; shift 2
if [ -x "$gems/bin/$1" ]; then GEM_HOME=$gems GEM_PATH=$gems; export GEM_HOME GEM_PATH; fi
exec "$@"`
		wrapped := append([]string{"-c", script, "sh", strings.Join(bins, ":"), path.Join(dir, "gems"), name}, args...)
		return vfs.CommandEnvironment(root, env, unset, stdin, "/bin/sh", wrapped...)
	}
	sep := string(os.PathListSeparator)
	managed := []string{"PATH=" + strings.Join(bins, sep) + sep + os.Getenv("PATH")}
	if found := findManaged(bins, name); found != "" {
		name = found
		if gems := filepath.Join(dir, "gems"); filepath.Dir(found) == filepath.Join(gems, "bin") {
			managed = append(managed, "GEM_HOME="+gems, "GEM_PATH="+gems)
		}
	}
	return vfs.CommandEnvironment(root, append(managed, env...), unset, stdin, name, args...)
}

// waitContext runs cmd, and kills it when ctx ends.
func waitContext(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}
