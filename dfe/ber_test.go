package dfe

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guiyones/folio/dfe/dfetest"
	"software.sslmate.com/src/go-pkcs12"
)

func TestLoadPFXBER(t *testing.T) {
	notAfter := time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC)
	der, pass := dfetest.NewCert(t, "11222333000181", notAfter)

	for name, enc := range map[string]*pkcs12.Encoder{
		"modern": pkcs12.Modern,
		// Same algorithms as the Java/BouncyCastle files that came in BER.
		"legacy": pkcs12.LegacyRC2,
	} {
		t.Run(name, func(t *testing.T) {
			ber := toBER(t, reencode(t, der, pass, enc), pass)
			if _, _, _, err := pkcs12.DecodeChain(ber, pass); err == nil {
				t.Fatal("the fixture is not BER: pkcs12 read it")
			}
			opensslAccepts(t, ber, pass, enc == pkcs12.LegacyRC2)

			c, err := LoadPFX(ber, pass)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if c.CNPJ != "11222333000181" || !c.NotAfter.Equal(notAfter) || c.Key == nil {
				t.Errorf("certificate = %+v", c)
			}

			if _, err := LoadPFX(ber, "senha-errada-123"); !errors.Is(err, ErrBadPassword) {
				t.Errorf("wrong password: err = %v, want ErrBadPassword", err)
			}
		})
	}
}

func TestPFXToDERRejectsTamperedContent(t *testing.T) {
	der, pass := dfetest.NewCert(t, "11222333000181", time.Now().AddDate(1, 0, 0))
	ber := toBER(t, der, pass)

	// Flip a byte inside the authenticated safe: the MAC no longer matches.
	i := len(ber) / 2
	ber[i] ^= 0xff

	if _, err := pfxToDER(ber, pass); err == nil {
		t.Fatal("a tampered file was accepted")
	}
}

func TestParseBERLimits(t *testing.T) {
	for name, in := range map[string][]byte{
		"empty":                {},
		"length past end":      {0x04, 0x05, 0x01},
		"no end of content":    {0x30, 0x80, 0x02, 0x01, 0x00},
		"indefinite primitive": {0x04, 0x80, 0x00, 0x00},
		"too deep":             bytes.Repeat([]byte{0x30, 0x80}, berMaxDepth+2),
	} {
		if _, _, err := parseBER(in, 0); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// reencode opens a PFX and packs it again with enc.
func reencode(t *testing.T, pfx []byte, pass string, enc *pkcs12.Encoder) []byte {
	t.Helper()
	key, leaf, chain, err := pkcs12.DecodeChain(pfx, pass)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	out, err := enc.Encode(key, leaf, chain, pass)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return out
}

// toBER rewrites a DER PFX the way Java writes it: indefinite lengths
// everywhere and octet strings cut in chunks, inside the authenticated safe
// too, with the MAC computed again over the BER bytes.
func toBER(t *testing.T, der []byte, pass string) []byte {
	t.Helper()

	pfx := mustParse(t, der)
	authSafe := pfx.children[1]
	raw := authSafe.children[1].children[0].content

	safe := mustParse(t, raw)
	for _, ci := range safe.children {
		explicit := ci.children[1]
		switch oidOf(t, ci.children[0]) {
		case oidData.String():
			inner := explicit.children[0]
			inner.content = berEncode(mustParse(t, inner.content))
		case oidEncryptedData.String():
			encrypted := explicit.children[0].children[1].children[2]
			chunked(encrypted)
		}
	}
	berSafe := berEncode(safe)
	authSafe.children[1].children[0].content = berSafe

	macData := pfx.children[2]
	var m pfxMacData
	if _, err := asn1.Unmarshal(derEncodeOrFatal(t, macData), &m); err != nil {
		t.Fatalf("mac data: %v", err)
	}
	bmp, err := bmpPassword(pass)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := pfxMAC(m, bmp, berSafe)
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	macData.children[0].children[1].content = digest

	return berEncode(pfx)
}

func mustParse(t *testing.T, b []byte) *berNode {
	t.Helper()
	n, rest, err := parseBER(b, 0)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parse: %v (%d bytes left)", err, len(rest))
	}
	return n
}

func oidOf(t *testing.T, n *berNode) string {
	t.Helper()
	var oid asn1.ObjectIdentifier
	if _, err := asn1.Unmarshal(derEncodeOrFatal(t, n), &oid); err != nil {
		t.Fatalf("oid: %v", err)
	}
	return oid.String()
}

func derEncodeOrFatal(t *testing.T, n *berNode) []byte {
	t.Helper()
	b, err := derEncode(n)
	if err != nil {
		t.Fatalf("der: %v", err)
	}
	return b
}

// chunked turns a primitive string into a constructed one made of 16-byte
// OCTET STRING pieces.
func chunked(n *berNode) {
	if n.constructed || len(n.content) <= 16 {
		return
	}
	for b := n.content; len(b) > 0; {
		k := min(16, len(b))
		n.children = append(n.children, &berNode{tag: asn1.TagOctetString, content: b[:k]})
		b = b[k:]
	}
	n.constructed, n.content = true, nil
}

// berEncode writes every constructed node with an indefinite length and every
// long OCTET STRING in chunks.
func berEncode(n *berNode) []byte {
	if n.class == asn1.ClassUniversal && n.tag == asn1.TagOctetString {
		chunked(n)
	}
	var out bytes.Buffer
	if !n.constructed {
		out.Write(identifier(n))
		out.Write(derLength(len(n.content)))
		out.Write(n.content)
		return out.Bytes()
	}
	out.Write(identifier(n))
	out.WriteByte(0x80)
	for _, c := range n.children {
		out.Write(berEncode(c))
	}
	out.Write([]byte{0, 0})
	return out.Bytes()
}

// opensslAccepts checks the fixture with an independent reader: openssl
// verifies the MAC and opens every bag.
func opensslAccepts(t *testing.T, pfx []byte, pass string, legacy bool) {
	t.Helper()
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Log("openssl não instalado; o MAC do BER não foi conferido por fora")
		return
	}
	path := filepath.Join(t.TempDir(), "ber.pfx")
	if err := os.WriteFile(path, pfx, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"pkcs12", "-in", path, "-noout", "-passin", "env:PFX_PASS"}
	if legacy {
		args = append(args, "-legacy")
	}
	cmd := exec.Command(openssl, args...)
	cmd.Env = append(os.Environ(), "PFX_PASS="+pass)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl refused the BER fixture: %v\n%s", err, out)
	}
}
