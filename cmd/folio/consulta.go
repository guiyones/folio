package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/guiyones/folio/dfe"
)

// consulta calls NFeDistribuicaoDFe once and prints and saves what came.
// It never repeats the call, so it can run beside the hourly scan of another
// tool only if that tool is paused.
func consulta(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("consulta", flag.ContinueOnError)
	pfxPath := fs.String("pfx", "", "arquivo .pfx do certificado A1 (obrigatório)")
	uf := fs.Int("uf", 41, "código IBGE da UF do interessado (41 = PR)")
	amb := fs.Int("amb", 2, "ambiente: 1 produção, 2 homologação")
	cnpj := fs.String("cnpj", "", "CNPJ consultado (padrão: o do certificado)")
	nsu := fs.Int64("nsu", 0, "último NSU já recebido")
	outDir := fs.String("out", "out", "pasta onde gravar os XMLs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pfxPath == "" {
		fs.Usage()
		return errors.New("--pfx é obrigatório")
	}

	data, err := os.ReadFile(*pfxPath)
	if err != nil {
		return fmt.Errorf("lendo o certificado: %w", err)
	}
	pass, err := pfxPassword()
	if err != nil {
		return err
	}
	cert, err := dfe.LoadPFX(data, pass)
	if err != nil {
		return err
	}
	if *cnpj == "" {
		*cnpj = cert.CNPJ
	}

	roots, err := sefazRoots()
	if err != nil {
		return err
	}
	client, err := dfe.NewClient(cert, dfe.Env(*amb), dfe.WithRootCAs(roots))
	if err != nil {
		return err
	}

	r, err := client.DistNSU(ctx, *uf, *cnpj, *nsu)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "cStat %s: %s\nultNSU %d, maxNSU %d, %d documento(s)\n", r.CStat, r.Motivo, r.UltNSU, r.MaxNSU, len(r.Docs))
	if len(r.Docs) == 0 {
		return nil
	}

	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		return fmt.Errorf("criando %s: %w", *outDir, err)
	}
	for _, d := range r.Docs {
		kind, err := dfe.KindOf(d.Schema)
		if err != nil {
			kind = "desconhecido"
		}
		fmt.Fprintf(out, "%15d  %-14s %s\n", d.NSU, kind, describe(kind, d.XML))

		name := filepath.Join(*outDir, fmt.Sprintf("%015d-%s.xml", d.NSU, kind))
		if err := os.WriteFile(name, d.XML, 0o600); err != nil {
			return fmt.Errorf("gravando %s: %w", name, err)
		}
	}
	return nil
}

// describe is a one-line summary of a document for the terminal.
func describe(kind dfe.Kind, xml []byte) string {
	var (
		n   dfe.Note
		err error
	)
	switch kind {
	case dfe.KindSummary:
		n, err = dfe.ParseSummary(xml)
	case dfe.KindFull:
		n, err = dfe.ParseFull(xml)
	case dfe.KindEventSummary, dfe.KindEvent:
		e, err := dfe.ParseEvent(kind, xml)
		if err != nil {
			return "não foi possível ler: " + err.Error()
		}
		return fmt.Sprintf("%s  evento %s %s", e.Key, e.Type, e.Description)
	default:
		return ""
	}
	if err != nil {
		return "não foi possível ler: " + err.Error()
	}
	return fmt.Sprintf("%s  %s  %s  R$ %d,%02d", n.Key, n.EmitCNPJ, n.EmitName, n.TotalCents/100, n.TotalCents%100)
}
