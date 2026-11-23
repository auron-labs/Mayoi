package highlight

import (
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"mayoi/internal/workspace"
)

// association is one entry of the fileAssociations setting.
type association struct {
	pattern string
	globs   workspace.Globs
	// anchored is whether the glob has a slash, so that it matches a path
	// and not just the file's name.
	anchored bool
	lexer    chroma.Lexer
}

// associations are the entries of the setting that name a language Chroma
// has, those with a slash first, each group by pattern.
var associations atomic.Pointer[[]association]

// SetAssociations sets the languages the person chose for files, as glob
// to language: a glob without a slash matches a file's name in any folder,
// one with a slash matches the end of its path, as a path from a folder
// does. A language is a lexer's name or alias in any case, and an entry
// that names none is left out.
func SetAssociations(m map[string]string) {
	var list []association
	for pattern, name := range m {
		l := lexerNamed(name)
		globs := workspace.ParseGlobs(pattern)
		if l == nil || len(globs) == 0 {
			continue
		}
		list = append(list, association{pattern, globs, strings.Contains(strings.Trim(pattern, "/"), "/"), l})
	}
	slices.SortFunc(list, func(a, b association) int {
		if a.anchored != b.anchored {
			if a.anchored {
				return -1
			}
			return 1
		}
		return strings.Compare(a.pattern, b.pattern)
	})
	associations.Store(&list)
}

// Associated returns the lexer the person chose for a file, nil for none.
func Associated(path string) chroma.Lexer {
	list := associations.Load()
	if list == nil || path == "" {
		return nil
	}
	slashed := filepath.ToSlash(path)
	base := filepath.Base(slashed)
	segs := strings.Split(strings.Trim(slashed, "/"), "/")
	for _, a := range *list {
		if !a.anchored {
			if a.globs.Match(base) {
				return a.lexer
			}
			continue
		}
		// The folder is not known here, so the pattern may start at any
		// of the path's folders.
		for i := range segs {
			if a.globs.Match(strings.Join(segs[i:], "/")) {
				return a.lexer
			}
		}
	}
	return nil
}

// lexerNamed returns the lexer of a language by the name or alias Chroma
// has for it, or Mayoi's own for Markdown and Vue, nil for none.
func lexerNamed(name string) chroma.Lexer {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "markdown", "md":
		return markdown()
	case "vue":
		return vue()
	}
	l := lexers.Get(name)
	if l == nil {
		l = lexers.Get(strings.ReplaceAll(name, " ", ""))
	}
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}
