package dfe

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// This file is a small XML tree plus Canonical XML 1.0 (inclusive, without
// comments: http://www.w3.org/TR/2001/REC-xml-c14n-20010315), which is what
// NF-e signatures use. It covers what NF-e documents contain: elements,
// attributes, namespaces and text. Comments and processing instructions are
// dropped, as canonicalization without comments does.

// element is a node of the tree. Prefixes are kept as written (RawToken), so
// namespaces are resolved by walking the parents.
type element struct {
	prefix, local string
	nsDecls       []xml.Attr // xmlns and xmlns:p declared here
	attrs         []xml.Attr // other attributes
	children      []any      // *element or text (string)
	parent        *element
}

// parseTree reads data into a tree and returns its root.
func parseTree(data []byte) (*element, error) {
	d := xml.NewDecoder(bytes.NewReader(data))

	var root, cur *element
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("lendo XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			el := &element{prefix: t.Name.Space, local: t.Name.Local, parent: cur}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
					el.nsDecls = append(el.nsDecls, a)
				} else {
					el.attrs = append(el.attrs, a)
				}
			}
			if cur == nil {
				if root != nil {
					return nil, errors.New("XML com mais de uma raiz")
				}
				root = el
			} else {
				cur.children = append(cur.children, el)
			}
			cur = el

		case xml.EndElement:
			if cur == nil || cur.prefix != t.Name.Space || cur.local != t.Name.Local {
				return nil, fmt.Errorf("XML com </%s> fora de lugar", qname(t.Name.Space, t.Name.Local))
			}
			cur = cur.parent

		case xml.CharData:
			if cur != nil {
				cur.children = append(cur.children, string(t))
			}
		}
	}

	if root == nil {
		return nil, errors.New("XML vazio")
	}
	if cur != nil {
		return nil, errors.New("XML incompleto")
	}
	return root, nil
}

// inScope returns the namespaces visible at e, prefix to URI ("" is the
// default namespace).
func (e *element) inScope() map[string]string {
	var chain []*element
	for n := e; n != nil; n = n.parent {
		chain = append(chain, n)
	}

	ns := map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		for _, d := range chain[i].nsDecls {
			prefix := ""
			if d.Name.Space == "xmlns" {
				prefix = d.Name.Local
			}
			ns[prefix] = d.Value
		}
	}
	return ns
}

// uri resolves prefix at e.
func (e *element) uri(prefix string) string {
	return e.inScope()[prefix]
}

// attr returns the value of the unprefixed attribute local.
func (e *element) attr(local string) (string, bool) {
	for _, a := range e.attrs {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// find returns the first element under e (e included), depth first, for
// which match is true.
func (e *element) find(match func(*element) bool) *element {
	if match(e) {
		return e
	}
	for _, c := range e.children {
		if el, ok := c.(*element); ok {
			if found := el.find(match); found != nil {
				return found
			}
		}
	}
	return nil
}

// child returns the first direct child named local in namespace uri.
func (e *element) child(uri, local string) *element {
	for _, c := range e.children {
		if el, ok := c.(*element); ok && el.local == local && el.uri(el.prefix) == uri {
			return el
		}
	}
	return nil
}

// text returns the concatenated text directly inside e.
func (e *element) text() string {
	var b strings.Builder
	for _, c := range e.children {
		if s, ok := c.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// canonicalize writes the canonical form of e and its descendants, skipping
// the element skip (the enveloped Signature) when it is not nil.
func canonicalize(e, skip *element) []byte {
	var b bytes.Buffer
	writeCanonical(&b, e, skip, map[string]string{})
	return b.Bytes()
}

// writeCanonical renders e. rendered holds the namespaces already declared
// by the nearest rendered ancestor; a namespace is declared again only when
// its value changes.
func writeCanonical(b *bytes.Buffer, e, skip *element, rendered map[string]string) {
	scope := e.inScope()

	// Namespace declarations that differ from the output parent, sorted by
	// prefix (the default namespace, with an empty prefix, comes first).
	var prefixes []string
	for p, uri := range scope {
		if prev, ok := rendered[p]; ok && prev == uri {
			continue
		}
		if !has(rendered, p) && p == "" && uri == "" {
			continue // xmlns="" at the top says nothing
		}
		prefixes = append(prefixes, p)
	}
	slices.Sort(prefixes)

	// Attributes sorted by namespace URI, then local name; unqualified
	// attributes have an empty URI and come first.
	attrs := slices.Clone(e.attrs)
	slices.SortFunc(attrs, func(x, y xml.Attr) int {
		ux, uy := attrURI(e, x), attrURI(e, y)
		if c := strings.Compare(ux, uy); c != 0 {
			return c
		}
		return strings.Compare(x.Name.Local, y.Name.Local)
	})

	b.WriteByte('<')
	b.WriteString(qname(e.prefix, e.local))
	for _, p := range prefixes {
		if p == "" {
			b.WriteString(` xmlns="`)
		} else {
			b.WriteString(` xmlns:` + p + `="`)
		}
		b.WriteString(escapeAttr(scope[p]))
		b.WriteByte('"')
	}
	for _, a := range attrs {
		b.WriteString(" " + qname(a.Name.Space, a.Name.Local) + `="` + escapeAttr(a.Value) + `"`)
	}
	b.WriteByte('>')

	for _, c := range e.children {
		switch n := c.(type) {
		case string:
			b.WriteString(escapeText(n))
		case *element:
			if n != skip {
				writeCanonical(b, n, skip, scope)
			}
		}
	}

	b.WriteString("</" + qname(e.prefix, e.local) + ">")
}

func has(m map[string]string, k string) bool {
	_, found := m[k]
	return found
}

func attrURI(e *element, a xml.Attr) string {
	if a.Name.Space == "" {
		return ""
	}
	if a.Name.Space == "xml" {
		return "http://www.w3.org/XML/1998/namespace"
	}
	return e.uri(a.Name.Space)
}

func qname(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }
