package highlight

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestAssociationsChooseTheLexer(t *testing.T) {
	t.Cleanup(func() { SetAssociations(nil) })
	SetAssociations(map[string]string{
		"*.foo":               "YAML",
		"Jenkinsfile":         "groovy",
		"config/*.conf":       "ini",
		"*.go":                "not a language",
		"docs/*.txt":          "Markdown",
		"/abs/only/*.special": "json",
	})
	cases := []struct{ path, want string }{
		{"/p/a.foo", "YAML"},
		{"/p/sub/Jenkinsfile", "Groovy"},
		{"/p/config/app.conf", "INI"},
		{"/p/other/app.conf", Name(Lexer("/p/other/app.conf"))},
		{"/p/docs/a.txt", "markdown"},
		{"/p/main.go", "Go"}, // an entry that names no language is ignored
	}
	for _, c := range cases {
		if got := Name(Lexer(c.path)); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
	if Associated("/p/main.go") != nil {
		t.Error("an unknown language is associated")
	}
}

func TestAssociationsBeatThePluginName(t *testing.T) {
	old := LexerOverride
	LexerOverride = func(string) chroma.Lexer { return lexers.Get("python") }
	t.Cleanup(func() { LexerOverride = old; SetAssociations(nil) })
	if got := Name(Lexer("x.foo")); got != "Python" {
		t.Fatalf("plugin lexer %q", got)
	}
	SetAssociations(map[string]string{"*.foo": "yaml"})
	if got := Name(Lexer("x.foo")); got != "YAML" {
		t.Errorf("with an association %q", got)
	}
	SetAssociations(nil)
	if got := Name(Lexer("x.foo")); got != "Python" {
		t.Errorf("after clearing %q", got)
	}
}
