package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"mayoi/internal/plugin"
	"mayoi/internal/remote"
	"mayoi/internal/vfs"
	"mayoi/locales"
)

// serverInstallTimeout is how long an install may take.
const serverInstallTimeout = 10 * time.Minute

// downloadScript downloads a server's release into $1/$2 and links its
// program, $5, into $1/bin. $3 is the URL, which its suffix says how to
// unpack, and $4 the program's path in an archive.
const downloadScript = `set -e
dir=$1 name=$2 url=$3 bin=$4 prog=$5
mkdir -p "$dir/bin"
tmp=$dir/$name.part
rm -rf "$tmp"
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT
if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$tmp/download" "$url"
elif command -v wget >/dev/null 2>&1; then wget -q -O "$tmp/download" "$url"
else echo "curl or wget is needed to download $url" >&2; exit 1; fi
case $url in
*.tar.gz|*.tgz) tar -xzf "$tmp/download" -C "$tmp" ;;
*.tar.xz) tar -xJf "$tmp/download" -C "$tmp" ;;
*.zip) unzip -q "$tmp/download" -d "$tmp" ;;
*.gz) bin=$prog; gzip -dc "$tmp/download" > "$tmp/$bin" ;;
*) bin=$prog; cp "$tmp/download" "$tmp/$bin" ;;
esac
rm -f "$tmp/download"
[ -f "$tmp/$bin" ] || { echo "$bin is not in $url" >&2; exit 1; }
chmod +x "$tmp/$bin"
rm -rf "$dir/$name"
mv "$tmp" "$dir/$name"
ln -sf "$dir/$name/$bin" "$dir/bin/$prog"`

// hostPlatform is the operating system and CPU of the machine of root, as
// a download's maps name them: "linux" or "darwin", and "x86_64" or
// "aarch64".
func hostPlatform(ctx context.Context, root string) (system, arch string, err error) {
	if !remote.Is(root) {
		system, arch = runtime.GOOS, runtime.GOARCH
	} else {
		cmd, err := vfs.Command(root, nil, nil, "uname", "-sm")
		if err != nil {
			return "", "", err
		}
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := waitContext(ctx, cmd); err != nil {
			return "", "", err
		}
		fields := strings.Fields(strings.ToLower(out.String()))
		if len(fields) != 2 {
			return "", "", fmt.Errorf("unexpected output of uname: %q", out.String())
		}
		system, arch = fields[0], fields[1]
	}
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return system, arch, nil
}

// installCommand is the program that installs the server of sc on the
// machine of root: the download of its release when there is one for the
// machine, else its install command in a shell.
func installCommand(ctx context.Context, root, dir, pluginName string, sc *plugin.ServerContrib, program string) (string, []string, error) {
	onWindows := !remote.Is(root) && runtime.GOOS == "windows"
	if sc.Download != nil && !onWindows {
		// A machine that cannot tell its platform is left to Install.
		if system, arch, err := hostPlatform(ctx, root); err == nil {
			if url, bin, ok := sc.Download.Resolve(system, arch); ok {
				return "/bin/sh", []string{"-c", downloadScript, "sh", dir, pluginName, url, bin, program}, nil
			}
		}
	}
	switch {
	case sc.Install == "":
		return "", nil, errors.New("there is nothing to install it with on this machine")
	case onWindows:
		return "cmd", []string{"/C", sc.Install}, nil
	}
	return "/bin/sh", []string{"-c", sc.Install}, nil
}

// runServerInstall installs the program of a server on the machine of
// root into the folder of the servers Mayoi installed, and returns what
// the install wrote. The error is the last line of that, or what ended it.
func runServerInstall(ctx context.Context, root string, p *plugin.Plugin, sc *plugin.ServerContrib, program string) (string, error) {
	dir, err := serversDir(ctx, root)
	if err != nil {
		return "", err
	}
	name, args, err := installCommand(ctx, root, dir, p.Name, sc, program)
	if err != nil {
		return "", err
	}
	cmd, err := vfs.CommandEnvironment(root, serverEnv(root, dir), nil, nil, name, args...)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := waitContext(ctx, cmd); err != nil {
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if last := strings.TrimSpace(lines[len(lines)-1]); last != "" && !errors.Is(err, context.DeadlineExceeded) {
			err = errors.New(last)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// installServerProgram installs the program of the server sc of the
// plugin p on the folder's machine, and turns the plugin on, whose
// servers start for the files open. One that could not install may be
// tried again.
func (a *app) installServerProgram(p *plugin.Plugin, sc *plugin.ServerContrib, program string) {
	root := a.root
	a.tell(note{message: locales.PluginsInstalling(p.Title())})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), serverInstallTimeout)
		defer cancel()
		out, err := runServerInstall(ctx, root, p, sc, program)
		level := "info"
		if err != nil {
			level = "error"
		}
		if out = strings.TrimSpace(out); out != "" {
			p.Log(level, out)
		}
		if err != nil {
			p.Log("error", "could not install "+program+": "+err.Error())
		}
		a.post(func() {
			if err != nil {
				a.tell(note{message: locales.LspInstallFailed(p.Title(), firstLine(err.Error())),
					label: locales.PluginsIndexRetry(), action: func() { a.installServerProgram(p, sc, program) }})
				return
			}
			a.tell(note{message: locales.PluginsInstalled(p.Title())})
			if plugins == nil {
				return
			}
			if !slices.Contains(plugins.enabled(), p) {
				plugins.setEnabled(p.Name, true)
				return
			}
			// On already, its server failed for the missing program.
			forgetFailedServers(p.Name)
			plugins.startServers()
		})
	}()
}
