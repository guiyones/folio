package dfe

import (
	"os/exec"
	"strings"
	"testing"
)

// tricky exercises what C14N changes: attribute order, namespace
// placement, redundant declarations, empty elements, escaping and text.
// (xmllint --c14n keeps comments, so the sample has none.)
const tricky = `<?xml version="1.0" encoding="UTF-8"?>
<a:root xmlns:a="urn:a" xmlns="urn:default" z="1" a:y="2" b="3 &amp; &lt;4&gt; &quot;5&quot;">
  <child xmlns="urn:default" empty=""/>
  <a:other xmlns:b="urn:b" b:k="v" k="w">texto &amp; &lt;mais&gt; &gt; fim</a:other>
  <reset xmlns=""><deep/></reset>
</a:root>`

func TestCanonicalizeMatchesXMLLint(t *testing.T) {
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint não instalado")
	}

	cmd := exec.Command(xmllint, "--c14n", "-")
	cmd.Stdin = strings.NewReader(tricky)
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("xmllint: %v", err)
	}

	root, err := parseTree([]byte(tricky))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := string(canonicalize(root, nil)); got != string(want) {
		t.Errorf("canonical form differs\n got: %s\nwant: %s", got, want)
	}
}

func TestCanonicalizeSubsetInheritsNamespaces(t *testing.T) {
	root, err := parseTree([]byte(`<evento xmlns="urn:nfe" versao="1.00"><infEvento Id="X"><a>1</a></infEvento></evento>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	inf := root.find(func(e *element) bool { return e.local == "infEvento" })

	want := `<infEvento xmlns="urn:nfe" Id="X"><a>1</a></infEvento>`
	if got := string(canonicalize(inf, nil)); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestParseTreeRejects(t *testing.T) {
	for _, bad := range []string{"", "<a>", "<a></b>", "<a/><b/>"} {
		if _, err := parseTree([]byte(bad)); err == nil {
			t.Errorf("parseTree(%q) accepted", bad)
		}
	}
}
