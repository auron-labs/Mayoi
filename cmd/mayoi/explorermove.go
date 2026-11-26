package main

import (
	"errors"
	"path/filepath"
	"slices"

	"mayoi/internal/bg"
	"mayoi/internal/remote"
	"mayoi/internal/vfs"
	"mayoi/locales"

	"github.com/egoist/mygo/ui"
)

// A file or a folder dragged from the explorer onto a row of it moves
// into the row's folder, or the folder of the row's file; onto the
// folder's row at the top, or below the rows, into the workspace's
// folder. Dragged with the rows chosen with it, all of them move. Each
// moves within its project alone, never over a file or a folder of its
// name, nor into itself, and its open documents follow it as they follow
// a rename.

// treeRootDrop makes e move a file or a folder of the explorer dropped on
// it into the workspace's folder, setting action to, and reports whether
// one that would is over it.
func (a *app) treeRootDrop(e ui.Element, action *func()) bool {
	if v, ok := ui.Drop[treeTake](e); ok {
		if paths := a.treePaths(v); paths != nil {
			*action = func() { a.dropInTree(paths, a.root) }
		}
	}
	v, ok := a.treeDragOver(e)
	return ok && a.canMove(a.treePaths(v), a.root)
}

// treePaths are the files and folders of this explorer that v drags: its
// row's, or those chosen with it, but those inside another of them, which
// go with that one. nil for what was dragged from elsewhere.
func (a *app) treePaths(v treeTake) []string {
	s := treeSource(v)
	if s == nil || s.from != a {
		return nil
	}
	return outermost(a.explorerPicked(s.path))
}

// canMove reports whether any of paths, dropped into dir, would move: it
// is not in dir already, and dir is neither it nor in it.
func (a *app) canMove(paths []string, dir string) bool {
	return dir != "" && slices.ContainsFunc(paths, func(path string) bool {
		return dir != path && dir != remote.Dir(path) && !remote.Contains(path, dir) && remote.HostOf(dir) == remote.HostOf(path)
	})
}

// dropInTree moves paths, dropped on the explorer, into dir. One dropped
// where it is, as on its own row or among its neighbors, stays, saying
// nothing; one dropped into itself stays, saying why, and the others move.
func (a *app) dropInTree(paths []string, dir string) {
	if dir == "" {
		return
	}
	var move []string
	for _, path := range paths {
		switch {
		case dir == path || dir == remote.Dir(path) || remote.HostOf(dir) != remote.HostOf(path):
		case remote.Contains(path, dir):
			a.fail("%s", locales.FailMove(filepath.Base(path), locales.ErrMoveIntoItself()))
		default:
			move = append(move, path)
		}
	}
	a.moveInto(move, dir)
}

// moveInto moves the files and folders of paths into dir, keeping their
// names, but each that another of its name is there. On a host they move
// in the background, one at a time, and one moving already does not move
// again: one at a time also keeps two of a name from going over each
// other.
func (a *app) moveInto(paths []string, dir string) {
	if !remote.Is(dir) {
		// All move before any is followed, here and on a host, so that
		// the folders read again after the first hold the others too.
		errs := make([]error, len(paths))
		for i, path := range paths {
			errs[i] = moveEntry(path, remote.Join(dir, filepath.Base(path)))
		}
		for i, path := range paths {
			a.moved(path, remote.Join(dir, filepath.Base(path)), errs[i])
		}
		return
	}
	paths = slices.DeleteFunc(slices.Clone(paths), func(path string) bool { return a.moving[path] })
	if len(paths) == 0 {
		return
	}
	if a.moving == nil {
		a.moving = map[string]bool{}
	}
	for _, path := range paths {
		a.moving[path] = true
	}
	bg.Go(func() {
		errs := make([]error, len(paths))
		for i, path := range paths {
			errs[i] = moveEntry(path, remote.Join(dir, filepath.Base(path)))
		}
		a.post(func() {
			for i, path := range paths {
				delete(a.moving, path)
				a.moved(path, remote.Join(dir, filepath.Base(path)), errs[i])
			}
			// The folder, read ahead before the moves, is read again, as
			// loading, unlike a poll: the rows chosen that went into it
			// stay chosen until they show.
			a.tree.Load(dir)
			a.tree.RefreshIn(dir)
		})
	})
}

// moveEntry renames path to to, unless a file or a folder is there.
func moveEntry(path, to string) error {
	if _, err := vfs.Lstat(to); err == nil {
		return errors.New(locales.ErrNameExists())
	}
	return vfs.Rename(path, to)
}

// moved has the documents of path follow it to to, shows it there,
// chosen, in its folder opened, or says why it did not move.
func (a *app) moved(path, to string, err error) {
	if err != nil {
		a.fail("%s", locales.FailMove(filepath.Base(path), err.Error()))
		return
	}
	a.renamed(path, to)
	if dir := remote.Dir(to); dir != a.root {
		a.files.Open.Add(dir)
	}
	if a.files.Open.Has(path) {
		a.files.Open.Remove(path)
		a.files.Open.Add(to)
	}
	a.selectInTree = to
}
