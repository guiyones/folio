package dfe

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"hash"
	"unicode/utf16"
)

// Some issuers (Java and BouncyCastle) deliver the PFX in BER: indefinite
// lengths and octet strings cut in chunks, inside the authenticated safe too.
// pkcs12 reads only DER, so pfxToDER rewrites the file in DER before it.
//
// The MAC covers the bytes of the authenticated safe, so it is checked over
// the original BER bytes, which also checks the password, and then computed
// again over the DER ones, with the same algorithm, salt and iterations.

const (
	berMaxDepth = 32
	// pfxMaxIterations caps the MAC key derivation, so a crafted file can't
	// hold a request for minutes. Real files use 1 to 10000.
	pfxMaxIterations = 1 << 20
)

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}

	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
)

// berNode is one BER element: a primitive one has content, a constructed one
// has children.
type berNode struct {
	class, tag  int
	constructed bool
	content     []byte
	children    []*berNode
}

// pfxMacData is the MacData of a PFX:
//
//	MacData ::= SEQUENCE { mac DigestInfo, macSalt OCTET STRING, iterations INTEGER DEFAULT 1 }
type pfxMacData struct {
	Mac struct {
		Algorithm pkix.AlgorithmIdentifier
		Digest    []byte
	}
	MacSalt    []byte
	Iterations int `asn1:"optional,default:1"`
}

// pfxToDER rewrites a BER PFX in DER, with the MAC computed again. A MAC
// that doesn't match the password gives ErrBadPassword.
//
//	PFX         ::= SEQUENCE { version INTEGER, authSafe ContentInfo, macData MacData }
//	ContentInfo ::= SEQUENCE { contentType OID, content [0] EXPLICIT ANY }
func pfxToDER(data []byte, password string) ([]byte, error) {
	pfx, rest, err := parseBER(data, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("sobram bytes depois do PFX")
	}
	if !isSequence(pfx) || len(pfx.children) != 3 {
		return nil, errors.New("o PFX não tem versão, conteúdo e MAC")
	}

	authSafe := pfx.children[1]
	content, err := contentInfoData(authSafe)
	if err != nil {
		return nil, fmt.Errorf("authSafe: %w", err)
	}
	raw, err := octets(content)
	if err != nil {
		return nil, fmt.Errorf("authSafe: %w", err)
	}

	macNode := pfx.children[2]
	macDER, err := derEncode(macNode)
	if err != nil {
		return nil, fmt.Errorf("macData: %w", err)
	}
	var mac pfxMacData
	if err := unmarshalAll(macDER, &mac); err != nil {
		return nil, fmt.Errorf("macData: %w", err)
	}

	bmp, err := bmpPassword(password)
	if err != nil {
		return nil, err
	}
	if bmp, err = checkPFXMAC(mac, bmp, raw); err != nil {
		return nil, err
	}

	safe, err := authSafeToDER(raw)
	if err != nil {
		return nil, fmt.Errorf("authSafe: %w", err)
	}
	digest, err := pfxMAC(mac, bmp, safe)
	if err != nil {
		return nil, err
	}

	*content = berNode{tag: asn1.TagOctetString, content: safe}
	macNode.children[0].children[1] = &berNode{tag: asn1.TagOctetString, content: digest}
	return derEncode(pfx)
}

// checkPFXMAC checks the MAC and returns the password bytes that matched.
// Like pkcs12, it also tries an empty password as no bytes at all, which
// some tools write instead of the two zero bytes.
func checkPFXMAC(mac pfxMacData, bmp, message []byte) ([]byte, error) {
	for _, p := range [][]byte{bmp, nil} {
		digest, err := pfxMAC(mac, p, message)
		if err != nil {
			return nil, err
		}
		if hmac.Equal(digest, mac.Mac.Digest) {
			return p, nil
		}
		if len(bmp) != 2 {
			break
		}
	}
	return nil, ErrBadPassword
}

// authSafeToDER rewrites the authenticated safe in DER. The SafeContents
// inside a data ContentInfo are rewritten too. Those inside an encryptedData
// can't be reached before decryption, so only the encrypted octets are joined
// into one primitive string.
//
//	AuthenticatedSafe   ::= SEQUENCE OF ContentInfo
//	EncryptedData       ::= SEQUENCE { version INTEGER, encryptedContentInfo EncryptedContentInfo }
//	EncryptedContentInfo ::= SEQUENCE { contentType OID, algorithm AlgorithmIdentifier, encryptedContent [0] IMPLICIT OCTET STRING }
func authSafeToDER(raw []byte) ([]byte, error) {
	safe, rest, err := parseBER(raw, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 || !isSequence(safe) {
		return nil, errors.New("não é uma SEQUENCE de ContentInfo")
	}

	for i, ci := range safe.children {
		if !isSequence(ci) || len(ci.children) != 2 {
			return nil, fmt.Errorf("ContentInfo %d malformado", i)
		}
		oid, err := nodeOID(ci.children[0])
		if err != nil {
			return nil, fmt.Errorf("ContentInfo %d: %w", i, err)
		}
		content, err := explicitChild(ci.children[1])
		if err != nil {
			return nil, fmt.Errorf("ContentInfo %d: %w", i, err)
		}

		switch {
		case oid.Equal(oidData):
			ber, err := octets(content)
			if err != nil {
				return nil, fmt.Errorf("ContentInfo %d: %w", i, err)
			}
			contents, rest, err := parseBER(ber, 0)
			if err != nil {
				return nil, fmt.Errorf("SafeContents %d: %w", i, err)
			}
			if len(rest) != 0 {
				return nil, fmt.Errorf("SafeContents %d: sobram bytes", i)
			}
			der, err := derEncode(contents)
			if err != nil {
				return nil, fmt.Errorf("SafeContents %d: %w", i, err)
			}
			*content = berNode{tag: asn1.TagOctetString, content: der}

		case oid.Equal(oidEncryptedData):
			if !isSequence(content) || len(content.children) != 2 ||
				!isSequence(content.children[1]) || len(content.children[1].children) != 3 {
				return nil, fmt.Errorf("EncryptedData %d malformado", i)
			}
			encrypted := content.children[1].children[2]
			if encrypted.class != asn1.ClassContextSpecific || encrypted.tag != 0 {
				return nil, fmt.Errorf("EncryptedData %d sem encryptedContent", i)
			}
			b, err := octets(encrypted)
			if err != nil {
				return nil, fmt.Errorf("EncryptedData %d: %w", i, err)
			}
			encrypted.constructed, encrypted.content, encrypted.children = false, b, nil

		default:
			return nil, fmt.Errorf("ContentInfo %d de tipo %s não suportado", i, oid)
		}
	}

	return derEncode(safe)
}

// contentInfoData returns the [0] EXPLICIT content of a data ContentInfo.
func contentInfoData(ci *berNode) (*berNode, error) {
	if !isSequence(ci) || len(ci.children) != 2 {
		return nil, errors.New("ContentInfo malformado")
	}
	oid, err := nodeOID(ci.children[0])
	if err != nil {
		return nil, err
	}
	if !oid.Equal(oidData) {
		return nil, fmt.Errorf("tipo %s não suportado (só PFX protegido por senha)", oid)
	}
	return explicitChild(ci.children[1])
}

func explicitChild(n *berNode) (*berNode, error) {
	if n.class != asn1.ClassContextSpecific || n.tag != 0 || !n.constructed || len(n.children) != 1 {
		return nil, errors.New("esperava [0] EXPLICIT")
	}
	return n.children[0], nil
}

func nodeOID(n *berNode) (asn1.ObjectIdentifier, error) {
	if n.class != asn1.ClassUniversal || n.tag != asn1.TagOID || n.constructed {
		return nil, errors.New("esperava um OID")
	}
	b, err := derEncode(n)
	if err != nil {
		return nil, err
	}
	var oid asn1.ObjectIdentifier
	if err := unmarshalAll(b, &oid); err != nil {
		return nil, err
	}
	return oid, nil
}

func isSequence(n *berNode) bool {
	return n.class == asn1.ClassUniversal && n.tag == asn1.TagSequence && n.constructed
}

// octets returns the bytes of a string, joining the pieces of a constructed
// one. The pieces are OCTET STRINGs, and may be cut again.
func octets(n *berNode) ([]byte, error) {
	if !n.constructed {
		return n.content, nil
	}
	var out []byte
	for _, c := range n.children {
		if c.class != asn1.ClassUniversal || c.tag != asn1.TagOctetString {
			return nil, errors.New("pedaço de string que não é OCTET STRING")
		}
		b, err := octets(c)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func unmarshalAll(b []byte, v any) error {
	rest, err := asn1.Unmarshal(b, v)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New("sobram bytes")
	}
	return nil
}

// parseBER reads one BER element and returns what comes after it.
func parseBER(b []byte, depth int) (*berNode, []byte, error) {
	if depth > berMaxDepth {
		return nil, nil, errors.New("BER aninhado demais")
	}
	if len(b) < 2 {
		return nil, nil, errors.New("BER truncado")
	}

	n := &berNode{class: int(b[0] >> 6), constructed: b[0]&0x20 != 0, tag: int(b[0] & 0x1f)}
	b = b[1:]
	if n.tag == 0x1f {
		n.tag = 0
		for i := 0; ; i++ {
			if len(b) == 0 || i == 4 {
				return nil, nil, errors.New("tag BER inválida")
			}
			c := b[0]
			b = b[1:]
			n.tag = n.tag<<7 | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
	}
	if len(b) == 0 {
		return nil, nil, errors.New("BER truncado")
	}

	l := int(b[0])
	b = b[1:]
	if l == 0x80 {
		if !n.constructed {
			return nil, nil, errors.New("comprimento indefinido num elemento primitivo")
		}
		for {
			if len(b) >= 2 && b[0] == 0 && b[1] == 0 {
				return n, b[2:], nil
			}
			c, rest, err := parseBER(b, depth+1)
			if err != nil {
				return nil, nil, err
			}
			n.children = append(n.children, c)
			b = rest
		}
	}
	if l > 0x80 {
		k := l & 0x7f
		if k > 4 || len(b) < k {
			return nil, nil, errors.New("comprimento BER inválido")
		}
		l = 0
		for _, c := range b[:k] {
			l = l<<8 | int(c)
		}
		b = b[k:]
	}
	if l > len(b) {
		return nil, nil, errors.New("comprimento BER passa do fim")
	}

	body, rest := b[:l], b[l:]
	if !n.constructed {
		n.content = body
		return n, rest, nil
	}
	for len(body) > 0 {
		c, r, err := parseBER(body, depth+1)
		if err != nil {
			return nil, nil, err
		}
		n.children = append(n.children, c)
		body = r
	}
	return n, rest, nil
}

// derEncode writes a node with definite, minimal lengths, and a constructed
// OCTET STRING as a primitive one. Other constructed universal strings don't
// appear in a PFX and are refused.
func derEncode(n *berNode) ([]byte, error) {
	if n.constructed && n.class == asn1.ClassUniversal {
		switch n.tag {
		case asn1.TagSequence, asn1.TagSet:
		case asn1.TagOctetString:
			b, err := octets(n)
			if err != nil {
				return nil, err
			}
			n = &berNode{tag: asn1.TagOctetString, content: b}
		default:
			return nil, fmt.Errorf("string BER construída de tag %d não suportada", n.tag)
		}
	}

	body := n.content
	if n.constructed {
		var buf bytes.Buffer
		for _, c := range n.children {
			b, err := derEncode(c)
			if err != nil {
				return nil, err
			}
			buf.Write(b)
		}
		body = buf.Bytes()
	}

	out := identifier(n)
	out = append(out, derLength(len(body))...)
	return append(out, body...), nil
}

func identifier(n *berNode) []byte {
	first := byte(n.class) << 6
	if n.constructed {
		first |= 0x20
	}
	if n.tag < 0x1f {
		return []byte{first | byte(n.tag)}
	}
	var tag []byte
	for t := n.tag; t > 0; t >>= 7 {
		tag = append([]byte{byte(t&0x7f) | 0x80}, tag...)
	}
	tag[len(tag)-1] &^= 0x80
	return append([]byte{first | 0x1f}, tag...)
}

func derLength(l int) []byte {
	if l < 0x80 {
		return []byte{byte(l)}
	}
	var b []byte
	for ; l > 0; l >>= 8 {
		b = append([]byte{byte(l)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

// bmpPassword encodes the password as PKCS#12 wants it: UTF-16 big-endian
// ended by two zero bytes.
func bmpPassword(password string) ([]byte, error) {
	out := make([]byte, 0, 2*len(password)+2)
	for _, r := range password {
		if r > 0xffff {
			return nil, errors.New("a senha tem um caractere que o PFX não aceita")
		}
		for _, u := range utf16.Encode([]rune{r}) {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return append(out, 0, 0), nil
}

// pfxMAC computes the PFX HMAC of message, with the key from the PKCS#12 KDF
// (RFC 7292, appendix B.2, ID 3).
func pfxMAC(m pfxMacData, password, message []byte) ([]byte, error) {
	var (
		h func() hash.Hash
		v int
	)
	switch alg := m.Mac.Algorithm.Algorithm; {
	case alg.Equal(oidSHA1):
		h, v = sha1.New, 64
	case alg.Equal(oidSHA256):
		h, v = sha256.New, 64
	case alg.Equal(oidSHA512):
		h, v = sha512.New, 128
	default:
		return nil, fmt.Errorf("algoritmo de MAC %s não suportado", alg)
	}
	if m.Iterations < 1 || m.Iterations > pfxMaxIterations {
		return nil, fmt.Errorf("%d iterações no MAC fora do limite", m.Iterations)
	}

	key := pkcs12KDF(h, v, m.MacSalt, password, m.Iterations, 3, h().Size())
	mac := hmac.New(h, key)
	mac.Write(message)
	return mac.Sum(nil), nil
}

// pkcs12KDF is the key derivation of RFC 7292, appendix B.2. v is the block
// size of the hash, in bytes.
func pkcs12KDF(h func() hash.Hash, v int, salt, password []byte, iterations int, id byte, size int) []byte {
	d := bytes.Repeat([]byte{id}, v)
	i := append(fillBlocks(salt, v), fillBlocks(password, v)...)

	var out []byte
	for {
		a := d
		a = append(a[:len(a):len(a)], i...)
		for range iterations {
			sum := h()
			sum.Write(a)
			a = sum.Sum(nil)
		}
		out = append(out, a...)
		if len(out) >= size {
			return out[:size]
		}

		// I_j = (I_j + B + 1) mod 2^v, with B made of copies of A.
		b := fillBlocks(a, v)[:v]
		for j := 0; j < len(i); j += v {
			carry := 1
			for k := v - 1; k >= 0; k-- {
				s := int(i[j+k]) + int(b[k]) + carry
				i[j+k], carry = byte(s), s>>8
			}
		}
	}
}

// fillBlocks repeats p up to the next multiple of v bytes. An empty p stays
// empty.
func fillBlocks(p []byte, v int) []byte {
	if len(p) == 0 {
		return nil
	}
	n := v * ((len(p) + v - 1) / v)
	return bytes.Repeat(p, (n+len(p)-1)/len(p))[:n]
}
