package dfe

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Algorithms of the NF-e signature (Manual de Orientação do Contribuinte).
const (
	nsDSig       = "http://www.w3.org/2000/09/xmldsig#"
	algC14N      = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315"
	algEnveloped = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
	algRSASHA1   = "http://www.w3.org/2000/09/xmldsig#rsa-sha1"
	algSHA1      = "http://www.w3.org/2000/09/xmldsig#sha1"
)

// SignEnveloped signs the element of doc whose Id attribute is refID and
// returns the document, in canonical form, with a <Signature> appended as
// the last child of that element's parent: the layout SEFAZ expects for
// infNFe and infEvento.
//
// The digest is SHA-1 over the canonical element (C14N 1.0, enveloped
// signature removed) and the signature is RSA PKCS #1 v1.5 over the canonical
// SignedInfo, which is deterministic: the same input and key give the same
// bytes.
func SignEnveloped(doc []byte, refID string, cert *Certificate) ([]byte, error) {
	if cert == nil || cert.Key == nil || cert.Leaf == nil {
		return nil, errors.New("certificado ausente")
	}

	root, err := parseTree(doc)
	if err != nil {
		return nil, err
	}

	ref := root.find(func(e *element) bool {
		id, ok := e.attr("Id")
		return ok && id == refID
	})
	if ref == nil {
		return nil, fmt.Errorf("elemento com Id %q não encontrado", refID)
	}
	if ref.parent == nil {
		return nil, errors.New("o elemento assinado não pode ser a raiz")
	}

	digest := sha1.Sum(canonicalize(ref, nil))

	sig, err := parseTree([]byte(`<Signature xmlns="` + nsDSig + `"><SignedInfo>` +
		`<CanonicalizationMethod Algorithm="` + algC14N + `"></CanonicalizationMethod>` +
		`<SignatureMethod Algorithm="` + algRSASHA1 + `"></SignatureMethod>` +
		`<Reference URI="#` + escapeAttr(refID) + `"><Transforms>` +
		`<Transform Algorithm="` + algEnveloped + `"></Transform>` +
		`<Transform Algorithm="` + algC14N + `"></Transform>` +
		`</Transforms><DigestMethod Algorithm="` + algSHA1 + `"></DigestMethod>` +
		`<DigestValue>` + base64.StdEncoding.EncodeToString(digest[:]) + `</DigestValue></Reference></SignedInfo>` +
		`<SignatureValue></SignatureValue>` +
		`<KeyInfo><X509Data><X509Certificate>` + base64.StdEncoding.EncodeToString(cert.Leaf.Raw) + `</X509Certificate></X509Data></KeyInfo>` +
		`</Signature>`))
	if err != nil {
		return nil, fmt.Errorf("montando a assinatura: %w", err)
	}
	sig.parent = ref.parent
	ref.parent.children = append(ref.parent.children, sig)

	// SignedInfo is canonicalized in place, inside the document, so the
	// namespaces it inherits are the ones a verifier will see.
	signedInfo := sig.child(nsDSig, "SignedInfo")
	sum := sha1.Sum(canonicalize(signedInfo, nil))
	value, err := rsa.SignPKCS1v15(nil, cert.Key, crypto.SHA1, sum[:])
	if err != nil {
		return nil, fmt.Errorf("assinando: %w", err)
	}

	sv := sig.child(nsDSig, "SignatureValue")
	sv.children = []any{base64.StdEncoding.EncodeToString(value)}

	return canonicalize(root, nil), nil
}

// VerifyEnveloped checks the first enveloped signature of doc against the
// certificate it carries: the algorithms, the digest of the referenced
// element and the RSA signature. It does not check who issued the
// certificate.
func VerifyEnveloped(doc []byte) error {
	root, err := parseTree(doc)
	if err != nil {
		return err
	}

	sig := root.find(func(e *element) bool { return e.local == "Signature" && e.uri(e.prefix) == nsDSig })
	if sig == nil {
		return errors.New("documento sem Signature")
	}

	signedInfo := sig.child(nsDSig, "SignedInfo")
	if signedInfo == nil {
		return errors.New("Signature sem SignedInfo")
	}
	if err := checkAlgorithm(signedInfo, "CanonicalizationMethod", algC14N); err != nil {
		return err
	}
	if err := checkAlgorithm(signedInfo, "SignatureMethod", algRSASHA1); err != nil {
		return err
	}

	reference := signedInfo.child(nsDSig, "Reference")
	if reference == nil {
		return errors.New("SignedInfo sem Reference")
	}
	if err := checkAlgorithm(reference, "DigestMethod", algSHA1); err != nil {
		return err
	}
	uri, _ := reference.attr("URI") // a missing URI is empty and refused below
	refID, found := strings.CutPrefix(uri, "#")
	if !found || refID == "" {
		return fmt.Errorf("Reference URI %q inválida", uri)
	}

	ref := root.find(func(e *element) bool {
		id, ok := e.attr("Id")
		return ok && id == refID
	})
	if ref == nil {
		return fmt.Errorf("elemento com Id %q não encontrado", refID)
	}

	want, err := decodeChild(reference, "DigestValue")
	if err != nil {
		return err
	}
	got := sha1.Sum(canonicalize(ref, sig))
	if string(got[:]) != string(want) {
		return errors.New("DigestValue não confere: o conteúdo assinado foi alterado")
	}

	certDER, err := decodeChild(findDSig(sig, "X509Certificate"), "")
	if err != nil {
		return fmt.Errorf("X509Certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("lendo X509Certificate: %w", err)
	}
	pub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return ErrNotRSA
	}

	value, err := decodeChild(sig, "SignatureValue")
	if err != nil {
		return err
	}
	sum := sha1.Sum(canonicalize(signedInfo, nil))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA1, sum[:], value); err != nil {
		return fmt.Errorf("SignatureValue não confere: %w", err)
	}
	return nil
}

func checkAlgorithm(parent *element, local, want string) error {
	el := parent.child(nsDSig, local)
	if el == nil {
		return fmt.Errorf("assinatura sem %s", local)
	}
	if alg, _ := el.attr("Algorithm"); alg != want { // missing is "" and refused
		return fmt.Errorf("%s %q não suportado", local, alg)
	}
	return nil
}

// decodeChild base64-decodes the text of parent's child local, or of parent
// itself when local is empty.
func decodeChild(parent *element, local string) ([]byte, error) {
	el := parent
	if local != "" && parent != nil {
		el = parent.child(nsDSig, local)
	}
	if el == nil {
		return nil, fmt.Errorf("assinatura sem %s", local)
	}
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(el.text()), ""))
	if err != nil {
		return nil, fmt.Errorf("%s em base64 inválido: %w", el.local, err)
	}
	return b, nil
}

func findDSig(e *element, local string) *element {
	return e.find(func(n *element) bool { return n.local == local && n.uri(n.prefix) == nsDSig })
}
