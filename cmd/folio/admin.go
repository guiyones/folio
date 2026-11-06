package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"uuid"

	"github.com/guiyones/folio/dfe"
	"github.com/guiyones/folio/internal/account"
	"github.com/guiyones/folio/internal/apiclient"
	"github.com/guiyones/folio/internal/platform/crypt"
	"github.com/guiyones/folio/internal/platform/db"
)

const adminUsage = `uso: folio admin <comando> [opções]

comandos:
  migrate                                    atualiza o banco
  client-create  --name                      cria um cliente da API
  key-create     --client                    cria uma chave de API (mostrada uma vez só)
  key-revoke     --prefix                    revoga uma chave pelo começo dela
  account-create --client --name --cnpj --uf --env [--nsu]
                                             cria uma conta ligada ao cliente
  account-link   --account --client          dá acesso à conta a outro cliente
  account-pause  --account                   pausa a varredura da conta
  account-resume --account                   retoma a varredura da conta
  cert-upload    --account --pfx             guarda o certificado (senha em FOLIO_PFX_PASSWORD ou no terminal)
  cert-delete    --account                   apaga o certificado da conta

Todos leem FOLIO_DB_URL; cert-upload também lê FOLIO_CERT_KEY.
`

// admin runs one administration command. Clients, keys and accounts are only
// created here, never through the API. Writes to out are messages for the
// operator; when the terminal is gone there is no one left to tell, so their
// errors are not checked.
func admin(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, adminUsage)
		return errors.New("falta o comando")
	}
	cmd, args := args[0], args[1:]

	fs := flag.NewFlagSet("admin "+cmd, flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		name      = fs.String("name", "", "nome")
		clientID  = fs.String("client", "", "ID do cliente da API")
		accountID = fs.String("account", "", "ID da conta")
		prefix    = fs.String("prefix", "", "começo da chave, como mostrado ao criar")
		cnpj      = fs.String("cnpj", "", "CNPJ, só dígitos")
		uf        = fs.Int("uf", 41, "código IBGE da UF (41 = PR)")
		env       = fs.Int("env", 2, "ambiente: 1 produção, 2 homologação")
		nsu       = fs.Int64("nsu", 0, "NSU de partida")
		pfxPath   = fs.String("pfx", "", "arquivo .pfx")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	url := os.Getenv("FOLIO_DB_URL")
	if url == "" {
		return errors.New("FOLIO_DB_URL não definida")
	}
	conn, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close() // closing a pool at exit only fails on connections already gone

	now := time.Now().UTC()
	accounts := account.NewStore(conn)
	clients := apiclient.NewStore(conn)

	switch cmd {
	case "migrate":
		if err := db.Migrate(ctx, conn); err != nil {
			return err
		}
		fmt.Fprintln(out, "banco atualizado")

	case "client-create":
		c, err := clients.Create(ctx, *name, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "cliente %s criado: %s\n", c.Name, c.ID)

	case "key-create":
		id, err := parseID("--client", *clientID)
		if err != nil {
			return err
		}
		key, err := clients.CreateKey(ctx, id, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s\n\nGuarde esta chave agora: ela não aparece de novo. Para revogar: --prefix %s\n", key, apiclient.Prefix(key))

	case "key-revoke":
		if err := clients.RevokeKey(ctx, strings.TrimSpace(*prefix), now); err != nil {
			return err
		}
		fmt.Fprintln(out, "chave revogada")

	case "account-create":
		id, err := parseID("--client", *clientID)
		if err != nil {
			return err
		}
		a, err := accounts.Create(ctx, account.NewAccount{
			Name:     *name,
			CNPJ:     *cnpj,
			UF:       *uf,
			Env:      dfe.Env(*env),
			StartNSU: *nsu,
			ClientID: id,
		}, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "conta %s criada: %s\n", a.Name, a.ID)

	case "account-link":
		aid, err := parseID("--account", *accountID)
		if err != nil {
			return err
		}
		cid, err := parseID("--client", *clientID)
		if err != nil {
			return err
		}
		if err := accounts.LinkClient(ctx, aid, cid); err != nil {
			return err
		}
		fmt.Fprintln(out, "cliente ligado à conta")

	case "account-pause", "account-resume":
		id, err := parseID("--account", *accountID)
		if err != nil {
			return err
		}
		paused := cmd == "account-pause"
		if err := accounts.SetPaused(ctx, id, paused); err != nil {
			return err
		}
		if paused {
			fmt.Fprintln(out, "conta pausada")
		} else {
			fmt.Fprintln(out, "conta retomada")
		}

	case "cert-upload":
		return certUpload(ctx, accounts, *accountID, *pfxPath, now, out)

	case "cert-delete":
		id, err := parseID("--account", *accountID)
		if err != nil {
			return err
		}
		if err := accounts.DeleteCertificate(ctx, id); err != nil {
			return err
		}
		fmt.Fprintln(out, "certificado apagado; a varredura da conta para até um novo envio")

	default:
		fmt.Fprint(out, adminUsage)
		return fmt.Errorf("comando %q desconhecido", cmd)
	}

	return nil
}

func certUpload(ctx context.Context, accounts *account.Store, accountID, pfxPath string, now time.Time, out io.Writer) error {
	id, err := parseID("--account", accountID)
	if err != nil {
		return err
	}
	if pfxPath == "" {
		return errors.New("--pfx é obrigatório")
	}

	box, err := crypt.FromEnv()
	if err != nil {
		return err
	}
	a, err := accounts.ByID(ctx, id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(pfxPath)
	if err != nil {
		return fmt.Errorf("lendo o certificado: %w", err)
	}
	pass, err := pfxPassword()
	if err != nil {
		return err
	}

	sc, err := account.UploadCertificate(ctx, accounts, box, a, data, pass, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "certificado de %s (%s) guardado, válido até %s\n", sc.Holder, sc.CNPJ, sc.NotAfter.Format("02/01/2006"))
	return nil
}

func parseID(flagName, s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil(), fmt.Errorf("%s é obrigatório", flagName)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("%s %q não é um ID válido", flagName, s)
	}
	return id, nil
}
