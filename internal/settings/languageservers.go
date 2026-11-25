package settings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// LanguageServer is a language server the person sets up in settings.json,
// started for the documents of its Languages, or whose path from the
// folder matches one of its Files (globs, as search's "files to include"),
// whichever the document meets, beside the other servers that serve it;
// an Exclusive one keeps them from it, but for the exclusive.
type LanguageServer struct {
	ID        string          `json:"id"`
	Command   string          `json:"command"`
	Args      []string        `json:"args,omitempty"`
	Options   json.RawMessage `json:"initializationOptions,omitempty"`
	Languages []string        `json:"languages,omitempty"`
	Files     []string        `json:"files,omitempty"`
	Exclusive bool            `json:"exclusive,omitempty"`
}

// ValidLanguageServers returns the entries of list that can run, in order,
// and what is wrong with each of the others: an entry needs an id, a
// command, languages or files, and an id no entry before it has.
func ValidLanguageServers(list []LanguageServer) (valid []LanguageServer, problems []string) {
	var seen []string
	for i, s := range list {
		var problem string
		switch {
		case strings.TrimSpace(s.ID) == "":
			problem = "has no id"
		case slices.Contains(seen, s.ID):
			problem = fmt.Sprintf("repeats the id %q", s.ID)
		case strings.TrimSpace(s.Command) == "":
			problem = "has no command"
		case len(s.Languages) == 0 && len(s.Files) == 0:
			problem = "has neither languages nor files"
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("languageServers[%d] %s", i, problem))
			continue
		}
		seen = append(seen, s.ID)
		valid = append(valid, s)
	}
	return valid, problems
}
