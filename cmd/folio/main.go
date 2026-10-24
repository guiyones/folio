// Command folio is the fiscal engine: it serves the API and the SEFAZ scan,
// administers clients and accounts, and runs one-off queries for proofs.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `uso: folio <modo> [opções]

modos:
  serve     sobe a API e a varredura
  admin     cadastra clientes, chaves, contas e certificados
  consulta  consulta a SEFAZ uma vez, sem banco (prova de conceito)

"folio <modo> -h" mostra as opções de cada modo.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, os.Args[2:])
	case "admin":
		err = admin(ctx, os.Args[2:], os.Stdout)
	case "consulta":
		err = consulta(ctx, os.Args[2:], os.Stdout)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "modo %q desconhecido\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
