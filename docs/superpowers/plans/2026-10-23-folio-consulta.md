# folio, parte de consulta: plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para implementar este plano tarefa por tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.
>
> **Estado em 2026-10-24:** o Claude executou o plano inteiro, a pedido do dono. Ficam abertas só as provas contra a SEFAZ de verdade (Tarefa 4, passos 2 e 3; Tarefa 10, passo 5), que dependem de um certificado real. O registro de execução, no fim do plano, lista as decisões tomadas no caminho.
>
> **Divisão do trabalho (decidida em 2026-10-24, depois substituída):**
> - **O dono do produto escreve o núcleo da SEFAZ em `dfe/`:** Tarefa 1 (passos 3 a 6, o `LoadPFX`), Tarefa 2, Tarefa 3 e Tarefa 10. O Claude revisa cada uma.
> - **O Claude escreve o resto:** o repositório e os auxiliares de teste em `dfe/dfetest/`, e as Tarefas 4 a 9, 11, 12 e 13.
> - **`dfe/env.go`:** o Claude criou só o tipo `Env` da Tarefa 2, porque as contas dependem dele.

**Objetivo:** um serviço em Go que busca na SEFAZ (NFeDistribuicaoDFe) as notas emitidas contra o CNPJ de cada conta, faz a Ciência da Operação, guarda os XMLs e entrega as notas por uma API com cursor.

**Arquitetura:**
- **`dfe/`:** a biblioteca do motor. É pura: sem banco, sem HTTP de entrada e sem nada de folio, e outros programas podem importá-la. Cuida de certificado, mTLS, SOAP, leitura dos documentos e assinatura XML. A emissão vai morar aqui também.
- **`internal/`**, organizado por assunto e não por camada. Cada assunto tem os próprios tipos, as regras e o acesso ao banco:
  - `account`: contas, certificado e o envio do certificado;
  - `apiclient`: clientes da API e as chaves;
  - `note`: documentos recebidos, notas, eventos e o cursor;
  - `scan`: o laço de varredura e a Ciência. Ele junta `account`, `note` e `dfe`;
  - `httpapi`: o contrato `openapi.yaml`, o código gerado a partir dele e os handlers, que só traduzem HTTP para chamadas aos assuntos;
  - `platform/db`: conexão, migrations, transação, erros de banco e o banco de teste;
  - `platform/crypt`: a cifra do certificado.
- **Dependências:** um assunto não importa `httpapi`, e `dfe` não importa nada de `internal`. O `scan` recebe a SEFAZ por uma interface, então os testes usam uma SEFAZ falsa.
- **Binário:** um só, `cmd/folio`, com os modos `serve`, `admin` e `consulta`.

```
folio/
├── cmd/folio/            main.go, serve.go, admin.go, consulta.go
├── dfe/                  cert, client, distdfe, docs, sign, evento
│   └── dfetest/          certificado de teste e SEFAZ falsa com mTLS
└── internal/
    ├── account/          account.go, store.go, certificate.go
    ├── apiclient/        apiclient.go, store.go
    ├── note/             note.go, store.go
    ├── scan/             scan.go
    ├── httpapi/          openapi.yaml, oapi.yaml, gen/api.gen.go, handlers
    └── platform/
        ├── db/           db.go, migrate.go, sql/migrate.sql, dbtest/
        └── crypt/        box.go
```

**Tecnologias:**
- Go 1.27 e a biblioteca padrão: `net/http` com padrões de rota, `log/slog`, `crypto/*` e `encoding/xml`.
- Postgres 18, com `github.com/jmoiron/sqlx` e `github.com/jackc/pgx/v5/stdlib`.
- `github.com/ardanlabs/darwin/v3` para as migrations.
- `software.sslmate.com/src/go-pkcs12` para ler o certificado.
- `github.com/oapi-codegen/oapi-codegen/v2` como `tool` no `go.mod`, gerando código para `net/http` puro, sem chi e sem gin.
- podman.

**Spec:** `docs/superpowers/specs/2026-10-23-folio-consulta-design.md`

## Restrições globais

- Módulo `github.com/guiyones/folio`, `go 1.27`, `mise.toml` com `go = "1.27"`.
- UUID pela stdlib (`uuid.NewV7()`), como no Cadim.
- Todo erro é embrulhado com contexto (`fmt.Errorf("...: %w", err)`), mapeado para uma resposta clara ou registrado. Nenhum `_ =` sem um comentário dizendo por quê.
- O certificado, a senha e a chave de cifra nunca aparecem em log, em resposta da API, em mensagem de erro, no banco sem cifra, em argumento de linha de comando ou em commit. O `.gitignore` bloqueia `*.pfx`, `*.p12`, `.env` e `out/`.
- Nada de `InsecureSkipVerify`. Se a cadeia da SEFAZ não for confiável pelo sistema, entra um arquivo de CA configurável (Tarefa 4).
- Toda escrita em notas e eventos de uma conta roda numa transação que tem a trava dessa conta. Por isso as funções de escrita de `note` recebem um `*sqlx.Tx`, e não abrem transação própria.
- A API segue o `openapi.yaml`. Uma mudança na API começa no contrato, depois vem o `make generate`, e só então o handler.
- Limite de consulta: depois de um 137, de um 656, de um erro ou de chegar ao `maxNSU`, a próxima varredura da conta é em `1h`.
- Gravar documentos e avançar o `ult_nsu` acontece na mesma transação.
- Uma conta por `(cnpj, ambiente)`.
- Conta de outro cliente da API responde 404.
- No SQL, use `CAST(x AS tipo)` em vez de `::`, como no Cadim.
- Material do Leandro (XMLs, certificado, script) nunca vai para o repositório. Os XMLs de teste são sintéticos.

## Pontos de atenção na revisão

1. **O cursor pode pular nota se duas escritas da mesma conta se cruzarem.** Se a varredura e a Ciência gravarem notas da mesma conta em transações paralelas, um `seq` menor pode ser gravado depois de um maior, e o cliente que já leu o maior nunca vê o menor. Por isso toda escrita em notas e eventos de uma conta acontece com a trava da conta (`FOR UPDATE`). O teste fica na Tarefa 9.
2. **O mesmo documento chega duas vezes, ou fora de ordem.** Depois de uma queda, a varredura repete NSUs já vistos. O `procNFe` pode chegar antes do `resNFe`. A gravação é idempotente por `(conta, nsu)` e por `(conta, chave)`. A situação nunca volta de "completa" para "resumo", e "cancelada" não volta atrás. Os testes ficam na Tarefa 8.
3. **Certificado ruim no envio.** Senha errada, arquivo que não é PFX, certificado sem CNPJ ICP-Brasil, vencido, ou de outro CNPJ. Cada caso dá uma mensagem clara e nenhum ecoa a senha. Os testes ficam nas Tarefas 1, 7 e 12.
4. **Resposta estranha da SEFAZ.** HTML no lugar de SOAP, SOAP Fault, timeout ou `retDistDFeInt` sem `cStat`. Cada caso vira erro, o `ult_nsu` não muda e a próxima varredura fica para daqui a uma hora. Os testes ficam nas Tarefas 3 e 9.
5. **O `ult_nsu` nunca volta atrás.** Se a SEFAZ responder com um `ultNSU` menor que o guardado, ou com 138 e o lote vazio, o folio guarda o maior. O teste fica na Tarefa 9.

---

## Fase 1: prova de conceito

### Tarefa 1: Repositório e certificado

**Arquivos:**
- Criar: `go.mod`, `mise.toml`, `makefile`, `.gitignore`, `README.md`, `zarf/compose/docker_compose.yaml`
- Criar: `dfe/cert.go`, `dfe/cert_test.go`
- Criar: `dfe/dfetest/cert.go`, que é um auxiliar de teste exportado

**Interfaces:**
- Produz:
  - `dfe.Certificate{TLS tls.Certificate; Key *rsa.PrivateKey; Leaf *x509.Certificate; Chain []*x509.Certificate; CNPJ, Holder string; NotAfter time.Time}`
  - `dfe.LoadPFX(data []byte, password string) (*Certificate, error)`
  - os erros `dfe.ErrBadPassword`, `dfe.ErrNoCNPJ`, `dfe.ErrNotRSA`
  - `dfetest.NewCert(t *testing.T, cnpj string, notAfter time.Time) (pfx []byte, password string)`

**Por quê:** tudo depende do certificado. Ele abre a conexão mTLS, assina o evento e diz de qual CNPJ ele é. No e-CNPJ ICP-Brasil, o CNPJ fica no SubjectAltName, num `otherName` com OID `2.16.76.1.3.3`.

- [x] **Passo 1: criar o repositório.**
  - `git init`, criar o `go.mod` e o `mise.toml`.
  - Criar o `.gitignore` com `*.pfx`, `*.p12`, `.env` e `out/`.
  - Criar o compose com um `postgres:18-alpine` na porta 5452 (para não brigar com o Cadim, que usa 5442) e senha `postgres`.
  - O `makefile` tem os alvos `dev-up`, `dev-down`, `test` (`go test ./...`) e `lint` (`go vet ./...`).
  - Criar o repositório no GitHub com `gh repo create guiyones/folio --private --source . --push`, depois do primeiro commit.
- [x] **Passo 2: escrever o auxiliar `dfetest.NewCert`.** Feito pelo Claude, em `dfe/dfetest/cert.go`, com testes em `dfe/dfetest/dfetest_test.go`.
  - Gera uma chave RSA de 2048 bits e um certificado autoassinado com CN `EMPRESA TESTE:<cnpj>`.
  - O SubjectAltName tem um `otherName` com OID `2.16.76.1.3.3` e o valor `<cnpj>` como `OCTET STRING`, montado com `encoding/asn1` em `ExtraExtensions`.
  - Empacota tudo em PFX com `pkcs12.Modern.Encode` e a senha `"teste"`.
- [x] **Passo 3: escrever os testes que falham.**

```go
func TestLoadPFX(t *testing.T) {
    pfx, pass := dfetest.NewCert(t, "11222333000181", time.Date(2027, 10, 1, 0, 0, 0, 0, time.UTC))
    c, err := dfe.LoadPFX(pfx, pass)
    // err == nil; c.CNPJ == "11222333000181"; c.Holder == "EMPRESA TESTE"
    // c.NotAfter igual à data; len(c.TLS.Certificate) >= 1; c.Key != nil
}
func TestLoadPFXWrongPassword(t *testing.T) { /* errors.Is(err, dfe.ErrBadPassword); a mensagem não contém a senha */ }
func TestLoadPFXGarbage(t *testing.T)       { /* []byte("não sou pfx") dá erro, e não é ErrBadPassword */ }
func TestLoadPFXNoCNPJ(t *testing.T)        { /* certificado sem o OID e sem ":<14 dígitos>" no CN dá errors.Is(err, dfe.ErrNoCNPJ) */ }
```

- [x] **Passo 4:** rodar `go test ./dfe/ -run LoadPFX`. Tem que FALHAR, porque `LoadPFX` ainda não existe.
- [x] **Passo 5: implementar `LoadPFX`.**
  - Usar `pkcs12.DecodeChain`.
  - O CNPJ sai primeiro do OID `2.16.76.1.3.3` e, se ele não existir, do sufixo `:<14 dígitos>` do CN.
  - `Holder` é o CN sem esse sufixo.
- [x] **Passo 6:** rodar `go test ./dfe/`. Tem que PASSAR.
- [x] **Passo 7: commit:** `feat: repositório e leitura do certificado A1`.

### Tarefa 2: Cliente SOAP e consulta por NSU

**Arquivos:**
- Criar: `dfe/client.go`, `dfe/soap.go`, `dfe/distdfe.go`, `dfe/distdfe_test.go`
- Criar: `dfe/dfetest/server.go`, uma SEFAZ falsa com TLS que exige certificado do cliente
- Criar: `dfe/testdata/ret_137.xml`, `ret_138.xml` e `fault.xml`, todos sintéticos

**Interfaces:**
- Consome: `*dfe.Certificate`, da Tarefa 1.
- Produz:
  - `type Env int` com `Production Env = 1` e `Homologation Env = 2`.
  - `type Endpoints struct{ DistDFe, RecepcaoEvento string }` e `DefaultEndpoints(env Env) Endpoints`, com os valores abaixo.
  - `NewClient(cert *Certificate, env Env, opts ...Option) (*Client, error)`, com as opções `WithEndpoints(Endpoints)`, `WithRootCAs(*x509.CertPool)` e `WithTimeout(time.Duration)`. O padrão de timeout é `30*time.Second`.
  - `(*Client).DistNSU(ctx context.Context, uf int, cnpj string, ultNSU int64) (DistResult, error)`.
  - `DistResult{CStat, Motivo string; UltNSU, MaxNSU int64; RespondedAt time.Time; Docs []Doc}` e `Doc{NSU int64; Schema string; XML []byte}`.
  - As constantes `CStatNoDocs = "137"`, `CStatDocsFound = "138"` e `CStatOveruse = "656"`.
  - `dfetest.NewServer(t, h dfetest.Handler) *dfetest.Server`, onde `Handler` é `func(action string, body []byte) (status int, resp []byte)`. O `Server` tem `URL`, `Roots` (para o `WithRootCAs`) e `Requests() []Request`, com `Action`, `Body` e `ClientCerts` de cada chamada. **Já existe:** foi escrito pelo Claude, junto com o `NewCert`.

**Endereços** (conferir no Portal da NF-e antes de produção):

| | DistDFe | RecepcaoEvento (AN) |
|---|---|---|
| Produção | `https://www1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx` | `https://www.nfe.fazenda.gov.br/NFeRecepcaoEvento4/NFeRecepcaoEvento4.asmx` |
| Homologação | `https://hom1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx` | `https://hom1.nfe.fazenda.gov.br/NFeRecepcaoEvento4/NFeRecepcaoEvento4.asmx` |

**Formato da mensagem:**
- **Envelope:** SOAP 1.2, com `Content-Type: application/soap+xml; charset=utf-8; action="http://www.portalfiscal.inf.br/nfe/wsdl/NFeDistribuicaoDFe/nfeDistDFeInteresse"`.
- **Corpo:** `nfeDistDFeInteresse` > `nfeDadosMsg` > `distDFeInt versao="1.01"` com `tpAmb`, `cUFAutor`, `CNPJ` e `distNSU/ultNSU`. O `ultNSU` vai com 15 dígitos e zeros à esquerda.
- **Resposta:** `retDistDFeInt`, com `cStat`, `xMotivo`, `dhResp`, `ultNSU`, `maxNSU` e `loteDistDFeInt/docZip` (atributos `NSU` e `schema`, conteúdo em base64 de um gzip).
- **Como achar o retorno:** ler pelo nome local do elemento, sem amarrar o caminho inteiro de namespaces do envelope.

- [x] **Passo 1: escrever a SEFAZ falsa.** Feito pelo Claude, em `dfe/dfetest/server.go`.
- [x] **Passo 2: escrever os testes que falham.**

```go
func TestDistNSUDocs(t *testing.T) {
    // a SEFAZ falsa responde ret_138.xml: ultNSU 000000000000002, maxNSU 000000000000005,
    // 2 docZip (um resNFe_v1.01.xsd, um procNFe_v4.00.xsd)
    // r.CStat == "138"; r.UltNSU == 2; r.MaxNSU == 5; len(r.Docs) == 2
    // r.Docs[0].NSU == 1; r.Docs[0].Schema == "resNFe_v1.01.xsd"; bytes.HasPrefix(r.Docs[0].XML, []byte("<resNFe"))
    // o corpo enviado tem <ultNSU>000000000000000</ultNSU>, <cUFAutor>41</cUFAutor>, <tpAmb>2</tpAmb>
}
func TestDistNSUNoDocs(t *testing.T)      { /* ret_137.xml: CStat "137", Docs vazio, err nil */ }
func TestDistNSUFault(t *testing.T)       { /* status 500 com fault.xml: o erro tem o motivo do Fault */ }
func TestDistNSUHTML(t *testing.T)        { /* 200 com "<html>": err != nil e diz que a resposta não é SOAP */ }
func TestDistNSUNoCStat(t *testing.T)     { /* retDistDFeInt sem cStat dá erro */ }
func TestDistNSUTimeout(t *testing.T)     { /* o handler dorme mais que WithTimeout(50ms): o erro vem embrulhado e errors.Is(err, context.DeadlineExceeded) ou net.Error Timeout */ }
func TestDistNSUNeedsClientCert(t *testing.T) { /* a SEFAZ falsa recebeu um certificado de cliente (r.TLS.PeerCertificates não está vazio) */ }
```

- [x] **Passo 3:** rodar `go test ./dfe/ -run DistNSU`. Tem que FALHAR.
- [x] **Passo 4: implementar `NewClient` e `DistNSU`.**
  - `NewClient` monta um `http.Client` com `tls.Config{Certificates: []tls.Certificate{cert.TLS}, RootCAs: roots, MinVersion: tls.VersionTLS12}`.
  - Montar o XML com `encoding/xml` ou com um template de texto. Os valores são todos números, e o CNPJ é validado como 14 dígitos antes.
  - Descompactar com `compress/gzip`.
- [x] **Passo 5:** rodar `go test ./dfe/`. Tem que PASSAR.
- [x] **Passo 6: commit:** `feat: consulta NFeDistribuicaoDFe por NSU`.

### Tarefa 3: Ler resumos, notas e eventos

**Arquivos:**
- Criar: `dfe/docs.go`, `dfe/docs_test.go`
- Criar: `dfe/testdata/resNFe.xml`, `procNFe.xml`, `resEvento_cancel.xml` e `procEventoNFe_ciencia.xml`, todos sintéticos, com a chave `41261011222333000181550010000001231000001234`

**Interfaces:**
- Produz:
  - `type Kind string`, com `KindSummary = "resNFe"`, `KindFull = "procNFe"`, `KindEventSummary = "resEvento"` e `KindEvent = "procEventoNFe"`.
  - `KindOf(schema string) (Kind, error)`. Ela pega o texto antes do primeiro `_`. Um schema desconhecido dá `ErrUnknownSchema`.
  - `type Situation string`, com `Authorized = "autorizada"`, `Denied = "denegada"` e `Cancelled = "cancelada"`. Vem do `cSitNFe` 1, 2 ou 3.
  - `type Note struct{ Key, EmitCNPJ, EmitName string; TotalCents int64; IssuedAt time.Time; Situation Situation }`.
  - `ParseSummary(xml []byte) (Note, error)`, para o `resNFe`.
  - `ParseFull(xml []byte) (Note, error)`, para o `procNFe`. Lê `infNFe/emit`, `ide/dhEmi`, `total/ICMSTot/vNF` e `protNFe/infProt/chNFe`. A `Situation` é `Authorized`.
  - `type Event struct{ Key, Type, Description string; Seq int; At time.Time }` e `ParseEvent(kind Kind, xml []byte) (Event, error)`.
  - As constantes `EventCancel = "110111"` e `EventCiencia = "210210"`.

**Por quê:** a varredura precisa saber o que é cada documento sem conhecer XML. Os valores em dinheiro viram centavos inteiros (`"1234.50"` vira `123450`), para não ter arredondamento de float.

- [x] **Passo 1: escrever os testes que falham.**

```go
func TestKindOf(t *testing.T) { /* "resNFe_v1.01.xsd" dá KindSummary; "procNFe_v4.00.xsd" dá KindFull; "xyz.xsd" dá ErrUnknownSchema */ }
func TestParseSummary(t *testing.T) {
    // Key de 44 dígitos; EmitCNPJ "99888777000166"; EmitName "DISTRIBUIDORA TESTE LTDA"
    // TotalCents 123450 (vNF 1234.50); IssuedAt == 2026-10-20T09:15:00-03:00; Situation Authorized
}
func TestParseFull(t *testing.T)        { /* mesma chave e mesmo total que o resumo */ }
func TestParseEventCancel(t *testing.T) { /* Type "110111"; Seq 1; Key igual */ }
func TestParseCents(t *testing.T)       { /* "0.10" dá 10; "5" dá 500; "1.5" dá 150; "abc" dá erro; "1.234" dá erro */ }
```

- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar com `encoding/xml`, usando structs com só os campos usados.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: leitura de resumo, nota completa e eventos`.

### Tarefa 4: `folio consulta` e prova na SEFAZ

**Arquivos:**
- Criar: `cmd/folio/main.go`, que só despacha os modos
- Criar: `cmd/folio/consulta.go`

**Interfaces:**
- Consome: `LoadPFX`, `NewClient`, `DistNSU`, `KindOf`, `ParseSummary`, `ParseFull` e `ParseEvent`.
- Produz: `folio consulta --pfx <arquivo> --uf 41 --amb 2 --cnpj <cnpj> [--nsu 0] [--out out/]`.
  - A senha vem de `FOLIO_PFX_PASSWORD`. Se ela estiver vazia, o comando pergunta no terminal sem eco (`golang.org/x/term`).
  - O `--cnpj` é opcional e, se faltar, usa o do certificado.
  - A raiz extra de CA vem de `FOLIO_SEFAZ_CA`, um caminho de PEM. Ela é opcional.

**Saída:** uma linha com cStat, motivo, ultNSU e maxNSU. Depois, uma linha por documento com NSU, tipo, chave, emitente e valor. Cada XML é gravado em `<out>/<nsu>-<tipo>.xml`. Chama a SEFAZ **uma vez só**, sem repetir.

- [x] **Passo 1:** implementar. O modo é escolhido por `os.Args[1]`, e cada modo tem o próprio `flag.FlagSet`. Um modo desconhecido mostra o uso e sai com código 2.
- [ ] **Passo 2: provar em homologação** com o certificado do Leandro, quando ele liberar, ou com um seu.
  - Esperado: cStat `137`, ou `138` se houver algo, e nenhum erro de TLS ou de leitura.
  - Se der `x509: certificate signed by unknown authority`, baixe a cadeia da AC da SEFAZ, aponte o `FOLIO_SEFAZ_CA` para ela e repita. Não use `InsecureSkipVerify`.
- [ ] **Passo 3: provar em produção (`--amb 1`)**, uma vez, com o script do Leandro pausado e longe da rodada de hora em hora dele.
  - Esperado: as notas dele aparecem.
  - Anote o cStat e o `ultNSU` no README, sem a chave de nenhuma nota.
- [x] **Passo 4: commit:** `feat: comando folio consulta`.

**Ponto de checagem:** com isso a parte mais arriscada (certificado, TLS, SOAP) está provada. Se algo aqui obrigar a mudar o design, pare e reveja a spec antes da Fase 2.

---

## Fase 2: banco e varredura

### Tarefa 5: Cofre do certificado

**Arquivos:**
- Criar: `internal/platform/crypt/box.go`, `internal/platform/crypt/box_test.go`

**Interfaces:**
- Produz:
  - `NewBox(keyID string, key []byte) (*Box, error)`. Uma chave com tamanho diferente de 32 bytes dá `ErrKeySize`.
  - `(*Box).Seal(plain []byte) ([]byte, error)`, `(*Box).Open(sealed []byte) ([]byte, error)` e `(*Box).KeyID() string`.
  - `FromEnv() (*Box, error)`. Lê `FOLIO_CERT_KEY` (base64 de 32 bytes) e `FOLIO_CERT_KEY_ID` (o padrão é `"k1"`). Se a chave faltar, o erro diz qual variável faltou, e nunca mostra o valor.

**Formato do selado:** nonce de 12 bytes, depois o texto cifrado com a tag (AES-256-GCM). O `keyID` vai como dado adicional autenticado, para um selado de uma chave não abrir com outra.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestBoxRoundTrip`: abrir o selado devolve o original.
  - `TestBoxNonceDiffers`: selar duas vezes o mesmo texto dá resultados diferentes.
  - `TestBoxTampered`: um byte trocado dá erro.
  - `TestBoxWrongKey`: abrir com outra chave dá erro.
  - `TestBoxKeySize`: uma chave de 16 bytes dá `ErrKeySize`.
  - `TestFromEnvMissing`: o erro cita `FOLIO_CERT_KEY`.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar com `crypto/aes`, `crypto/cipher` e `crypto/rand`.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: cofre AES-GCM para o certificado`.

### Tarefa 6: Banco, contas e certificados

**Arquivos:**
- Criar: `internal/platform/db/db.go`, com `Open`, `WithTx` e o mapeamento de erros
- Criar: `internal/platform/db/migrate.go` e `internal/platform/db/sql/migrate.sql`, este embutido com `//go:embed`
- Criar: `internal/platform/db/dbtest/dbtest.go`
- Criar: `internal/account/account.go` (tipos e erros), `internal/account/store.go`, `internal/account/store_test.go`

**Interfaces:**
- Produz em `db`:
  - `Open(url string) (*sqlx.DB, error)` e `Migrate(ctx, db) error`.
  - `WithTx(ctx, db, fn func(tx *sqlx.Tx) error) error`, que dá rollback se `fn` devolver erro.
  - Os erros `db.ErrNotFound` e `db.ErrDuplicate`. A violação de UNIQUE é mapeada pelo código `23505` do pgx.
- Produz em `account`:
  - `account.NewStore(db *sqlx.DB) *Store`.
  - Os erros `account.ErrNotFound` e `account.ErrDuplicate`. O store traduz os erros de `db` para estes, e quem usa `account` não precisa conhecer `db`.
  - `type Account struct{ ID uuid.UUID; Name, CNPJ string; UF int; Env dfe.Env; UltNSU int64; NextScanAt time.Time; LastScanAt *time.Time; LastCStat, LastMessage string; Paused, AutoCiencia bool; CienciaCutoff time.Time; CreatedAt time.Time }`.
  - `type NewAccount struct{ Name, CNPJ string; UF int; Env dfe.Env; StartNSU int64; ClientID uuid.UUID }`. A conta nasce com `AutoCiencia` ligada, `CienciaCutoff` igual a agora e `NextScanAt` igual a agora.
  - `(*Store).Create(ctx, NewAccount, now time.Time) (Account, error)`. Se `(cnpj, env)` já existe, dá `ErrDuplicate`. Um campo inválido dá `ErrInvalid`, e um cliente inexistente dá `ErrUnknownClient`.
  - `ByID(ctx, id)`, `ForClient(ctx, clientID, id)`, que dá `ErrNotFound` quando a conta não é do cliente, e `ListForClient(ctx, clientID)`.
  - `LinkClient(ctx, accountID, clientID)` e `SetPaused(ctx, id, paused bool)`.
  - `type StoredCert struct{ Holder, CNPJ string; NotAfter, UploadedAt time.Time; KeyID string; PFX, Password []byte }`. Os dois últimos já vêm selados.
  - `PutCertificate(ctx, accountID, StoredCert)`, que substitui o anterior, `Certificate(ctx, accountID) (StoredCert, error)` e `DeleteCertificate(ctx, accountID)`.
  - `dbtest.New(t) *sqlx.DB`.
    - Lê `FOLIO_TEST_DB_URL` e pula o teste se a variável estiver vazia.
    - Cria um banco `test_<hex aleatório>`, roda as migrations e apaga o banco no `t.Cleanup`.
    - O alvo `make test` define `FOLIO_TEST_DB_URL=postgres://postgres:postgres@localhost:5452/postgres?sslmode=disable`.

**Migração 1.01** (versão do darwin, `-- Version: 1.01`):

```sql
CREATE TABLE clients (id UUID PRIMARY KEY, name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL);
CREATE TABLE api_keys (id UUID PRIMARY KEY, client_id UUID NOT NULL REFERENCES clients(id),
  hash BYTEA NOT NULL UNIQUE, prefix TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ);
CREATE TABLE accounts (id UUID PRIMARY KEY, name TEXT NOT NULL, cnpj CHAR(14) NOT NULL CHECK (cnpj ~ '^[0-9]{14}$'),
  uf SMALLINT NOT NULL, env SMALLINT NOT NULL CHECK (env IN (1,2)), ult_nsu BIGINT NOT NULL DEFAULT 0,
  next_scan_at TIMESTAMPTZ NOT NULL, last_scan_at TIMESTAMPTZ, last_cstat TEXT NOT NULL DEFAULT '',
  last_message TEXT NOT NULL DEFAULT '', paused BOOLEAN NOT NULL DEFAULT FALSE,
  auto_ciencia BOOLEAN NOT NULL DEFAULT TRUE, ciencia_cutoff TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT accounts_cnpj_env_key UNIQUE (cnpj, env));
CREATE TABLE client_accounts (client_id UUID NOT NULL REFERENCES clients(id), account_id UUID NOT NULL REFERENCES accounts(id),
  PRIMARY KEY (client_id, account_id));
CREATE TABLE certificates (account_id UUID PRIMARY KEY REFERENCES accounts(id), holder TEXT NOT NULL, cnpj CHAR(14) NOT NULL,
  not_after TIMESTAMPTZ NOT NULL, key_id TEXT NOT NULL, pfx BYTEA NOT NULL, password BYTEA NOT NULL, uploaded_at TIMESTAMPTZ NOT NULL);
```

- [x] **Passo 1: escrever os testes que falham.**
  - `TestCreateAccount`: os campos voltam, e o `ult_nsu` é igual ao `StartNSU`.
  - `TestCreateAccountDuplicate`: o mesmo CNPJ no mesmo ambiente dá `ErrDuplicate`. No outro ambiente funciona.
  - `TestAccountForClient`: a conta de outro cliente dá `ErrNotFound`.
  - `TestLinkClient`: dois clientes veem a mesma conta.
  - `TestCertificateReplace`: o segundo `PutCertificate` substitui o primeiro.
- [x] **Passo 2:** `make dev-up`, depois rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: contas e certificados no Postgres`.

### Tarefa 7: Comando `admin`

**Arquivos:**
- Criar: `cmd/folio/admin.go`
- Criar: `internal/apiclient/apiclient.go`, `internal/apiclient/store.go`, `internal/apiclient/store_test.go`
- Criar: `internal/account/certificate.go`, `internal/account/certificate_test.go`

**Interfaces:**
- Consome: `account.Store`, da Tarefa 6, e `crypt.Box`, da Tarefa 5.
- Produz em `apiclient` (`apiclient.NewStore(db) *Store`):
  - `CreateClient(ctx, name string) (Client, error)`.
  - `CreateKey(ctx, clientID uuid.UUID) (plain string, err error)`. Gera `"fk_" + base64.RawURLEncoding(32 bytes aleatórios)`, guarda o `sha256` do texto e um `prefix` com os 8 primeiros caracteres, e devolve o texto só desta vez.
  - `ClientByKey(ctx, plain string) (Client, error)`. Uma chave revogada ou desconhecida dá `apiclient.ErrNotFound`.
  - `RevokeKey(ctx, prefix string) error`.
- Produz os subcomandos de `folio admin` (todos leem `FOLIO_DB_URL`, e o `cert-upload` lê também `FOLIO_CERT_KEY`):
  - `migrate`
  - `client-create --name`, que imprime o ID
  - `key-create --client`, que imprime a chave e avisa que ela não aparece de novo
  - `key-revoke --prefix`
  - `account-create --client --name --cnpj --uf --env [--nsu]`
  - `account-link --account --client`
  - `account-pause --account` e `account-resume --account`
  - `cert-upload --account --pfx <arquivo>`, com a senha vinda de `FOLIO_PFX_PASSWORD` ou da pergunta no terminal
  - `cert-delete --account`

**Função compartilhada com a API** (em `internal/account/certificate.go`): `account.UploadCertificate(ctx, st *Store, box *crypt.Box, a Account, pfx []byte, password string, now time.Time) (StoredCert, error)`.
- Abre o PFX com `LoadPFX`.
- Recusa nestes casos, sempre sem ecoar a senha:
  - `ErrCNPJMismatch{Cert, Account string}`, quando o CNPJ do certificado é diferente do da conta;
  - `ErrExpired`, quando `NotAfter` é anterior a `now`.
- Sela o PFX e a senha e grava.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestCreateKeyAndLookup`: a chave devolvida acha o cliente, uma chave alterada não acha, e uma chave revogada não acha.
  - `TestKeyStoredHashed`: a coluna `hash` não contém o texto da chave.
  - Em `account`:
    - `TestUploadMismatch`: a mensagem traz os dois CNPJs e não traz a senha.
    - `TestUploadExpired`.
    - `TestUploadWrongPassword`: dá `errors.Is(err, dfe.ErrBadPassword)`.
    - `TestUploadOK`: depois de abrir o selado com o `Box`, o PFX é igual ao original.
    - `TestUploadStoredSealed`: lendo a coluna `pfx` direto no banco, ela não é igual ao PFX original.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar o `apiclient`, o envio do certificado e o `admin`.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR. Depois, à mão: criar um cliente, uma chave e uma conta de homologação, e subir o seu certificado.
- [x] **Passo 5: commit:** `feat: comando admin, chaves de API e envio de certificado`.

### Tarefa 8: Documentos, notas e cursor

**Arquivos:**
- Modificar: `internal/platform/db/sql/migrate.sql`, criando a versão 1.02
- Criar: `internal/note/note.go` (tipos `NoteRow` e `EventRow`, as situações e os erros), `internal/note/store.go`, `internal/note/store_test.go`

**Migração 1.02:**

```sql
CREATE SEQUENCE note_seq;
CREATE TABLE dfe_docs (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id), nsu BIGINT NOT NULL,
  schema TEXT NOT NULL, xml BYTEA NOT NULL, received_at TIMESTAMPTZ NOT NULL, CONSTRAINT dfe_docs_nsu_key UNIQUE (account_id, nsu));
CREATE TABLE notes (id UUID PRIMARY KEY, account_id UUID NOT NULL REFERENCES accounts(id), key CHAR(44) NOT NULL,
  emit_cnpj TEXT NOT NULL DEFAULT '', emit_name TEXT NOT NULL DEFAULT '', total_cents BIGINT NOT NULL DEFAULT 0,
  issued_at TIMESTAMPTZ, status TEXT NOT NULL CHECK (status IN ('resumo','completa','cancelada')),
  summary_doc_id UUID REFERENCES dfe_docs(id), full_doc_id UUID REFERENCES dfe_docs(id),
  ciencia TEXT NOT NULL DEFAULT 'pendente' CHECK (ciencia IN ('pendente','feita','falhou','nao_aplica')),
  ciencia_attempts INT NOT NULL DEFAULT 0, ciencia_next_at TIMESTAMPTZ, seq BIGINT NOT NULL,
  CONSTRAINT notes_key_key UNIQUE (account_id, key));
CREATE INDEX notes_account_seq ON notes (account_id, seq);
CREATE TABLE note_events (id UUID PRIMARY KEY, note_id UUID NOT NULL REFERENCES notes(id), type TEXT NOT NULL,
  seq INT NOT NULL, description TEXT NOT NULL DEFAULT '', at TIMESTAMPTZ NOT NULL, origin TEXT NOT NULL CHECK (origin IN ('sefaz','folio')),
  cstat TEXT NOT NULL DEFAULT '', doc_id UUID REFERENCES dfe_docs(id), CONSTRAINT note_events_key UNIQUE (note_id, type, seq, origin));
```

**Interfaces** (`note.NewStore(db) *Store`; as funções de escrita recebem um `*sqlx.Tx` que já tem a trava da conta, da Tarefa 9):
- `SaveDoc(ctx, tx, accountID, d dfe.Doc, now) (docID uuid.UUID, isNew bool, err error)`. Um NSU repetido devolve o ID existente com `isNew == false`.
- `ApplySummary(ctx, tx, accountID, docID, n dfe.Note, cutoff time.Time, auto bool) error`.
  - Faz upsert pela chave.
  - Se a nota for nova: o `status` é `resumo`, ou `cancelada` se `n.Situation == Cancelled`. A `ciencia` é `pendente` se `auto` estiver ligado e `n.IssuedAt` não for anterior a `cutoff`. Senão é `nao_aplica`.
  - Se a nota já for `completa`, só preenche o `summary_doc_id`.
- `ApplyFull(ctx, tx, accountID, docID, n dfe.Note) error`. Faz upsert, põe o `status` em `completa` (a não ser que já esteja `cancelada`) e preenche os campos e o `full_doc_id`. Se a nota for nova, a `ciencia` é `nao_aplica`, porque ela já chegou completa.
- `ApplyEvent(ctx, tx, accountID, docID, e dfe.Event, origin string, cstat string) error`.
  - Se a nota ainda não existir, cria com `status` `resumo` e só a chave.
  - O evento `110111` põe o `status` em `cancelada`.
  - Um evento repetido não duplica.
- Toda mudança numa linha de `notes` faz `seq = nextval('note_seq')` no mesmo comando.
- `NotesAfter(ctx, accountID uuid.UUID, after int64, limit int) ([]NoteRow, error)`. Usa `WHERE account_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, e o `limit` fica entre 1 e 500. `NoteRow` tem os campos de `notes` e mais `HasFull bool`.
- `NoteForClient(ctx, clientID, noteID) (NoteRow, error)`, que dá `note.ErrNotFound` para nota de conta de outro cliente, `NoteXML(ctx, noteID) (xml []byte, kind string, err error)` (o completo se houver, senão o resumo) e `NoteEvents(ctx, noteID)`.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestSummaryThenFull`: o `seq` cresce a cada mudança, o `status` vai de `resumo` para `completa` e `HasFull` fica verdadeiro.
  - `TestFullThenSummary`: continua `completa`, e a `ciencia` é `nao_aplica`.
  - `TestCancelSticks`: depois do cancelamento, um `ApplyFull` não tira o `cancelada`.
  - `TestSaveDocIdempotent`: o mesmo NSU duas vezes dá uma linha só.
  - `TestCienciaCutoff`: uma nota emitida antes do corte fica `nao_aplica`, uma emitida depois fica `pendente`, e com `auto=false` fica `nao_aplica`.
  - `TestNotesAfter`: o cursor só traz o que mudou, e o limite é respeitado.
  - `TestNoteForOtherClient`: dá `ErrNotFound`.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar com `INSERT ... ON CONFLICT (account_id, key) DO UPDATE`. As regras de situação vão num `CASE` no próprio SQL.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: documentos, notas e cursor de mudanças`.

### Tarefa 9: Varredura

**Arquivos:**
- Criar: `internal/scan/scan.go`, `internal/scan/scan_test.go`
- Modificar: `internal/account/store.go`, para incluir `LockDue` e `FinishScan`
- Criar: `cmd/folio/serve.go`, que por enquanto só roda o laço

**Interfaces:**
- Consome: as Tarefas 2, 3, 5, 6 e 8.
- Produz:
  - `type SEFAZ interface { DistNSU(ctx, uf int, cnpj string, ult int64) (dfe.DistResult, error); Ciencia(ctx, cnpj, key string, at time.Time) (dfe.EventResult, error) }`. O `Ciencia` só é usado a partir da Tarefa 11.
  - `type Dialer func(cert *dfe.Certificate, env dfe.Env) (SEFAZ, error)`. A produção usa `dfe.NewClient`.
  - `type Scanner struct{ Accounts *account.Store; Notes *note.Store; Box *crypt.Box; Dial Dialer; Now func() time.Time; Log *slog.Logger }`.
  - `(*Scanner).RunOnce(ctx) (ran bool, err error)`. Pega uma conta vencida e devolve `false` quando não há nenhuma.
  - `(*Scanner).Loop(ctx, tick time.Duration)`. Chama `RunOnce` até ele devolver `false` e espera o `tick`. Erros vão para o log e não derrubam o laço.
  - Em `account`, `LockDue(ctx, now) (*sqlx.Tx, Account, bool, error)` faz o `SELECT ... FROM accounts a JOIN certificates c ... WHERE NOT paused AND next_scan_at <= $1 ORDER BY next_scan_at LIMIT 1 FOR UPDATE OF a SKIP LOCKED`.
  - Em `account`, `FinishScan(ctx, tx, id, ult int64, next time.Time, cstat, msg string, now) error`.

**Constantes:** `Wait = time.Hour`, `MaxPages = 20`, `CertWarn = 30 * 24 * time.Hour` e `Tick = time.Minute`.

**Regras de `RunOnce`:**
1. **Pegar a conta.** `LockDue` abre a transação e trava a conta. Tudo abaixo roda nessa transação, e o commit é no final.
2. **Certificado.** Abre e lê o certificado.
   - Se o `NotAfter` já passou, faz `FinishScan` com `next = now + Wait`, `msg = "certificado vencido"`, e para. Não chama a SEFAZ.
   - Se faltam `CertWarn` ou menos, registra um `slog.LevelWarn` com a conta e a data de vencimento, e segue a varredura.
3. **Páginas.** Repete até `MaxPages` vezes:
   - Chama `DistNSU(ult)`.
   - **Se der erro:** rollback, abre uma transação nova só para `FinishScan(next = now + Wait, cstat = "", msg = <erro>)` e devolve o erro. Os documentos daquela rodada são perdidos e o `ult_nsu` não muda. Na próxima rodada, os NSUs voltam.
   - **Para cada documento:** `SaveDoc`. Se ele for novo, aplica conforme o `KindOf`. Um schema desconhecido só é guardado e vai para o log.
   - **Avança o ult:** `ult = max(ult, r.UltNSU)`.
   - **Quando para:** com `137` ou `656`, ou quando `ult >= r.MaxNSU`, encerra com `next = now + Wait`. Um `656` também vai para o log com `slog.LevelWarn`.
4. **Fim das páginas.** Se as `MaxPages` acabaram com o `ult` ainda abaixo do `max`, encerra com `next = now`, e o próximo tick continua.
5. **Fechamento.** `FinishScan` e commit.

- [x] **Passo 1: escrever os testes que falham**, com uma SEFAZ falsa em memória que segue um roteiro de respostas e um relógio fixo.
  - `TestScanPagesUntilMax`: com respostas `138` (ult 2, max 4) e depois `138` (ult 4, max 4), são duas chamadas, o `ult_nsu` termina em 4 e o `next_scan_at` é `now+1h`.
  - `TestScan137Waits`: um `137` dá `next = now+1h` e uma chamada só.
  - `TestScan656Waits`: igual, e o `last_cstat` fica `"656"`.
  - `TestScanErrorKeepsNSU`: um erro na segunda página dá `ult_nsu` igual ao inicial, nenhum `dfe_docs` gravado, o `last_message` com o erro e `next = now+1h`.
  - `TestScanNSUNeverGoesBack`: uma resposta com ult menor que o guardado mantém o guardado.
  - `TestScanMaxPages`: 20 páginas sem chegar ao max dão `next == now`.
  - `TestScanExpiredCert`: zero chamadas à SEFAZ e `last_message` igual a "certificado vencido".
  - `TestScanSkipsPausedAndNotDue`: `RunOnce` devolve `false`.
  - `TestScanLockSerializes`: duas goroutines chamam `RunOnce` ao mesmo tempo para uma conta só. Uma delas devolve `false`, e a SEFAZ falsa recebe uma rodada só.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar. No `serve.go`, ler `FOLIO_DB_URL` e `FOLIO_CERT_KEY`, rodar `Migrate`, depois `Loop(ctx, Tick)`, e parar no `SIGINT`/`SIGTERM` com `signal.NotifyContext`.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR. Depois, à mão, `folio serve` com a conta de homologação: a varredura roda uma vez e espera uma hora.
- [x] **Passo 5: commit:** `feat: varredura com limite de uma hora e trava por conta`.

---

## Fase 3: Ciência da Operação

### Tarefa 10: Assinatura XML e evento de Ciência

**Arquivos:**
- Criar: `dfe/sign.go`, `dfe/sign_test.go`, `dfe/evento.go`, `dfe/evento_test.go`
- Criar: `dfe/testdata/ret_evento_135.xml`, `ret_evento_573.xml` e `ret_evento_297.xml`

**Interfaces:**
- Produz:
  - `SignEnveloped(doc []byte, refID string, cert *Certificate) ([]byte, error)`.
    - Assina o elemento cujo atributo `Id` é `refID`.
    - Coloca `<Signature xmlns="http://www.w3.org/2000/09/xmldsig#">` como último filho do pai desse elemento.
    - Usa C14N `http://www.w3.org/TR/2001/REC-xml-c14n-20010315`, transforms `enveloped-signature` e C14N, digest SHA-1, `rsa-sha1` e `X509Data/X509Certificate` com o certificado folha.
  - `VerifyEnveloped(doc []byte) error`, que só serve aos testes e à depuração.
  - `(*Client).Ciencia(ctx, cnpj, key string, at time.Time) (EventResult, error)`.
  - `EventResult{CStat, Motivo, Protocol string; Duplicate bool; XML []byte}`. O `XML` é o `procEventoNFe` montado: o evento assinado mais o `retEvento`.
  - `type RejectedError struct{ CStat, Motivo string }`.

**Evento:**
- `envEvento versao="1.00"` com `idLote` numérico.
- Dentro, `evento versao="1.00"` > `infEvento Id="ID210210<chave>01"`, com os campos:
  - `cOrgao` 91, `tpAmb`, `CNPJ` e `chNFe`;
  - `dhEvento` no formato `2006-01-02T15:04:05-07:00`, no horário de Brasília (`time.FixedZone("BRT", -3*60*60)`);
  - `tpEvento` 210210, `nSeqEvento` 1, `verEvento` 1.00;
  - `detEvento versao="1.00"` > `descEvento` "Ciencia da Operacao".
- **SOAP 1.2:** `nfeDadosMsg` com namespace `http://www.portalfiscal.inf.br/nfe/wsdl/NFeRecepcaoEvento4`.
- **Retornos:**
  - Lote `128` com `retEvento/cStat` `135` é sucesso.
  - `573` (duplicidade de evento) também é sucesso, com `Duplicate = true`.
  - Qualquer outro `cStat` dá `*RejectedError`.

**Por quê:** a mesma assinatura vai servir para a NF-e e a NFC-e depois. O teste que vale é o de ida e volta, que assina e verifica de forma independente. A prova final é a SEFAZ de homologação aceitar a assinatura: ela responde que a nota não existe, mas não responde `297`, que é assinatura inválida.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestSignRoundTrip`: assinar um `envEvento` e `VerifyEnveloped` passa.
  - `TestSignTamperFails`: trocar um dígito da chave depois de assinar faz `VerifyEnveloped` falhar.
  - `TestSignDeterministic`: a mesma entrada e a mesma chave dão o mesmo `SignatureValue`, porque a RSA PKCS#1 v1.5 é determinística.
  - `TestCiencia135`, `TestCiencia573Duplicate` e `TestCiencia297Rejected`, todos contra a SEFAZ falsa. O teste do 297 dá `errors.As(err, &RejectedError)` com `CStat` `"297"`.
  - `TestCienciaBody`: o corpo enviado tem `Id="ID210210<chave>01"`, `<cOrgao>91</cOrgao>` e passa no `VerifyEnveloped`.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3: implementar.**
  - Decida entre usar o `github.com/russellhaering/goxmldsig` ou escrever a canonicalização para este formato fixo.
  - Se for usar a biblioteca, verifique que o resultado bate com o C14N inclusivo e que a assinatura fica no lugar que a SEFAZ espera.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [ ] **Passo 5: provar em homologação.** Rodar uma Ciência com uma chave qualquer de 44 dígitos.
  - Esperado: uma rejeição diferente de `297`, o que prova que a assinatura foi aceita.
  - Anote o cStat no README.
- [x] **Passo 6: commit:** `feat: assinatura XML-DSig e evento de Ciência da Operação`.

### Tarefa 11: Ciência na varredura

**Arquivos:**
- Modificar: `internal/scan/scan.go`, `internal/scan/scan_test.go`
- Modificar: `internal/note/store.go`, para incluir `CienciaDue` e `MarkCiencia`

**Interfaces:**
- Em `note`:
  - `CienciaDue(ctx, tx, accountID, now, limit int) ([]NoteRow, error)`. Busca as notas com `ciencia = 'pendente' AND (ciencia_next_at IS NULL OR ciencia_next_at <= now)`.
  - `MarkCiencia(ctx, tx, noteID, ok bool, now time.Time, next *time.Time) error`.
    - Com sucesso, a `ciencia` vira `feita`.
    - Com falha, os `attempts` sobem um. Ao chegar a `CienciaMaxAttempts`, a `ciencia` vira `falhou`. Senão, `ciencia_next_at` recebe `next`.
    - Mexe no `seq`.
- Constantes: `CienciaPerRun = 20`, `CienciaMaxAttempts = 5` e `CienciaRetry = time.Hour`.

**Regras:**
- Ao final da etapa 3 do `RunOnce`, quando a conta não estiver pausada, a varredura chama `Ciencia` para até `CienciaPerRun` notas devidas, na mesma transação e com a mesma trava da conta.
- Com sucesso, inclusive `Duplicate`:
  - não grava nada em `dfe_docs`, que é só o que veio pelo NSU;
  - registra o evento pelo `ApplyEvent`, com `origin = "folio"`, `doc_id` nulo, o `cstat` e a descrição "Ciencia da Operacao";
  - chama `MarkCiencia(ok=true)`.
- Com `RejectedError` ou erro de rede: `MarkCiencia(ok=false, next = now + CienciaRetry)`. Vai para o log e **não** derruba a varredura.

**Guardar o XML do evento:** acrescente à migração 1.03 a coluna `note_events.xml BYTEA`, que pode ser nula, e grave o `EventResult.XML` nela.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestScanSendsCiencia`: um resumo novo pendente gera uma chamada `Ciencia`, a nota fica `feita` e o evento tem `origin` `folio`.
  - `TestScanCienciaDuplicateIsSuccess`.
  - `TestScanCienciaRetries`: a quinta falha deixa a nota em `falhou`. Entre as falhas, a Ciência só é tentada de novo depois do `ciencia_next_at`.
  - `TestScanCienciaFailureKeepsScan`: o `ult_nsu` avança mesmo com a Ciência falhando.
  - `TestScanNoCienciaBeforeCutoff`: uma nota antiga não gera chamada.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: Ciência da Operação automática na varredura`.

---

## Fase 4: API

### Tarefa 12: Contrato, autenticação e contas

**Arquivos:**
- Criar: `internal/httpapi/openapi.yaml`, o contrato de toda a API, incluindo as rotas de notas da Tarefa 13
- Criar: `internal/httpapi/oapi.yaml`, a configuração do gerador
- Gerar: `internal/httpapi/gen/api.gen.go`, que é commitado, como o `_templ.go` do Cadim
- Criar: `internal/httpapi/httpapi.go`, `internal/httpapi/auth.go`, `internal/httpapi/accounts.go`, `internal/httpapi/accounts_test.go`
- Modificar: `makefile`, com o alvo `generate`; `go.mod`, com o `tool`; `cmd/folio/serve.go`, que passa a subir o HTTP em `FOLIO_ADDR` (o padrão é `:8080`) junto com o laço

**Por quê:** a API terá clientes em Go (Cadim) e em Ruby (beep). O contrato vem primeiro e o código segue ele. O `strict-server` gera uma interface com tipos de entrada e saída por rota, então um handler que devolve algo fora do contrato não compila. O beep pode gerar o cliente Ruby a partir do mesmo arquivo.

**Contrato:**
- OpenAPI `3.0.3`, porque é a versão que o `oapi-codegen` v2 suporta por completo.
- `securitySchemes.bearer` (`type: http`, `scheme: bearer`), aplicado a tudo.
- Um schema `Error` (`{error: string}`), usado em todas as respostas 4xx e 5xx.
- `ScanTooSoon` estende `Error` com `next_scan_at`. A resposta 429 declara o cabeçalho `Retry-After`.
- Os schemas `Account`, `Certificate`, `Note`, `Event` e as listas, com os campos abaixo e na Tarefa 13, todos em `snake_case`, com `required` explícito e `nullable: true` onde cabe.
- Os limites de `limit` (1 a 500) e de `after` (>= 0) ficam no próprio contrato.

**Gerador** (`oapi.yaml`):

```yaml
package: gen
output: internal/httpapi/gen/api.gen.go
generate:
  models: true
  std-http-server: true
  strict-server: true
```

- `go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@<versão estável>`.
- `make generate` roda `go tool oapi-codegen -config internal/httpapi/oapi.yaml internal/httpapi/openapi.yaml`, e `make test` depende de `generate`.
- **Validação das requisições:** o middleware `github.com/oapi-codegen/nethttp-middleware` (`OapiRequestValidatorWithOptions`) valida as requisições contra o contrato. Ele devolve 400 com o corpo `Error`. A verificação de segurança desse middleware fica desligada (`AuthenticationFunc` devolve nil), porque quem cuida da chave é o `auth.go`.

**Interfaces:**
- `httpapi.New(d Deps) http.Handler`, com `type Deps struct{ Accounts *account.Store; Notes *note.Store; Clients *apiclient.Store; Box *crypt.Box; Now func() time.Time; Log *slog.Logger }`.
- Por dentro, um `server` implementa `gen.StrictServerInterface`, e `gen.HandlerFromMux(gen.NewStrictHandler(server, nil), mux)` monta as rotas num `http.ServeMux`.
- A ordem dos middlewares é: recuperação de pânico, com log e 500; autenticação; validação; rotas.
- **Autenticação:**
  - O cabeçalho é `Authorization: Bearer fk_...`.
  - Sem cabeçalho ou com chave inválida, responde `401 {"error":"chave de API inválida"}`.
  - O cliente vai no contexto.
- **Erros:** o corpo é sempre `{"error": "<mensagem em pt-BR>"}`. `account.ErrNotFound` e `note.ErrNotFound` viram 404. Um erro inesperado vira 500 com `"erro interno"`, e o detalhe vai só para o log.
- **Rotas:**
  - `GET /v1/accounts` responde `{"accounts":[Account]}`.
  - `GET /v1/accounts/{id}` responde um `Account`.
  - `PUT /v1/accounts/{id}/certificate` recebe um multipart com os campos `pfx` (arquivo, até 1 MB) e `password`. Usa o `account.UploadCertificate`. O corpo é limitado com `http.MaxBytesReader`.
    - Responde `200` com o `certificate`.
    - Responde `422` com a mensagem de `ErrBadPassword`, `ErrCNPJMismatch`, `ErrExpired` ou PFX inválido.
  - `POST /v1/accounts/{id}/pause` e `POST /v1/accounts/{id}/resume` respondem `200` com o `Account`.
  - `POST /v1/accounts/{id}/scan`:
    - Se `next_scan_at` ainda não passou, responde `429` com `{"error":"...","next_scan_at":"..."}` e o cabeçalho `Retry-After` em segundos.
    - Se já passou, responde `202`.
    - Uma conta pausada responde `409`.
- **JSON da conta:**
  - Campos: `id`, `name`, `cnpj`, `uf`, `env` (`"producao"` ou `"homologacao"`), `paused`, `auto_ciencia`, `ult_nsu`, `next_scan_at`, `last_scan_at`, `last_cstat` e `last_message`.
  - Mais `certificate`, que é `null` ou `{holder, cnpj, not_after, uploaded_at, expiring_soon, expired}`. O `expiring_soon` é verdadeiro quando faltam `CertWarn` ou menos.
  - Datas em RFC 3339.

- [x] **Passo 0: escrever o `openapi.yaml` inteiro** (contas e notas) e o `oapi.yaml`, e rodar `make generate`. Esperado: `gen/api.gen.go` compila. Se o contrato tiver erro, o gerador recusa.
- [x] **Passo 1: escrever os testes que falham**, com `httptest`, banco de teste e um relógio fixo.
  - `TestAuthMissing` e `TestAuthRevoked` respondem 401.
  - `TestAccountsOnlyOwn`: o cliente A não vê a conta do B, e o `GET` da conta do B dá 404.
  - `TestCertificateUploadOK`, `TestCertificateUploadMismatch` (422, sem a senha no corpo), `TestCertificateUploadWrongPassword` (422) e `TestCertificateTooBig` (413 ou 422).
  - `TestAccountJSONNeverHasSecrets`: o corpo não contém `pfx`, `password` nem a senha.
  - `TestScanTooSoon`: 429 com `Retry-After`. `TestScanPaused`: 409.
  - `TestExpiringSoon`: com um certificado que vence em 10 dias, `expiring_soon` é verdadeiro.
  - `TestUnknownRoute404` e `TestBadUUID400`: o corpo é sempre `Error`.
  - `TestPanicIs500`: um handler que entra em pânico devolve 500 `{"error":"erro interno"}`.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar os métodos de contas do `StrictServerInterface`. Os métodos de notas devolvem 501 por enquanto e ficam para a Tarefa 13.
- [x] **Passo 4:** rodar os testes. Têm que PASSAR.
- [x] **Passo 5: commit:** `feat: contrato OpenAPI e API de contas com chave por cliente`.

### Tarefa 13: Notas, XML, eventos e Ciência à mão

**Arquivos:**
- Criar: `internal/httpapi/notes.go`, `internal/httpapi/notes_test.go`
- Modificar: `README.md`

**Interfaces** (já estão no `openapi.yaml` da Tarefa 12; aqui só se implementam os métodos):
- `GET /v1/accounts/{id}/notes?after=<seq>&limit=<n>`.
  - O `after` padrão é 0 e o `limit` padrão é 100, no máximo 500. Um valor inválido dá 400 pela validação do contrato.
  - Responde `{"notes":[Note],"next_after":<seq da última nota, ou o after recebido se a lista vier vazia>}`.
  - `Note` tem `id`, `key`, `emit_cnpj`, `emit_name`, `total_cents`, `issued_at`, `status`, `ciencia`, `has_full_xml` e `seq`.
- `GET /v1/notes/{id}/xml` responde `200` com `Content-Type: application/xml` e o cabeçalho `X-Folio-Kind: procNFe` ou `resNFe`.
- `GET /v1/notes/{id}/events` responde `{"events":[{type, seq, description, at, origin, cstat}]}`.
- `POST /v1/notes/{id}/ciencia`:
  - Só funciona em conta com `auto_ciencia` desligada e nota com `ciencia` em `nao_aplica` ou `falhou`.
  - Põe a `ciencia` em `pendente`, zera os `attempts` e o `ciencia_next_at`, e responde `202`. A próxima varredura envia.
  - Em qualquer outro caso, responde `409` com o motivo.
  - Mexe no `seq`, então precisa rodar com a trava da conta. Para isso, acrescente `account.(*Store).Lock(ctx, tx *sqlx.Tx, id uuid.UUID) error`, que faz `SELECT ... FROM accounts WHERE id = $1 FOR UPDATE`. O handler abre a transação com `db.WithTx`, trava a conta e chama `note.(*Store).RequestCiencia(ctx, tx, noteID)`.

- [x] **Passo 1: escrever os testes que falham.**
  - `TestNotesCursor`: com 3 notas, `limit=2` traz 2, e `after=next_after` traz a última.
  - `TestNotesOtherClient`: 404.
  - `TestNoteXMLFullOverSummary`: devolve o completo quando ele existe.
  - `TestManualCiencia`: 202 e a nota fica `pendente`. Com `auto_ciencia` ligada, dá 409.
  - `TestNotesBadLimit`: 400.
- [x] **Passo 2:** rodar os testes. Têm que FALHAR.
- [x] **Passo 3:** implementar. No README, apontar o `internal/httpapi/openapi.yaml` como a documentação da API, e descrever as variáveis de ambiente (`FOLIO_DB_URL`, `FOLIO_CERT_KEY`, `FOLIO_CERT_KEY_ID`, `FOLIO_ADDR`, `FOLIO_SEFAZ_CA`, `FOLIO_PFX_PASSWORD`), os modos do binário, como gerar a chave (`openssl rand -base64 32`) e o roteiro de uso: criar o cliente, a chave, a conta e o certificado, depois rodar o `serve` e consultar as notas.
- [x] **Passo 4:** rodar `make lint && make test`. Tudo tem que PASSAR.
- [x] **Passo 5: commit:** `feat: API de notas com cursor, XML, eventos e Ciência à mão`.

---

## Depois deste plano

- O projeto 2, o Cadim consumindo o folio, tem design próprio. Ele cobre o casamento de produtos, a categoria financeira, o catálogo e o planograma.
- O folio entra em produção para o Leandro só quando o Cadim estiver pronto. Nesse dia, o script dele para de consultar a SEFAZ e a conta é criada com `--nsu 0`.
- A emissão (NFC-e e NF-e) também terá um design próprio e vai reaproveitar `dfe.Certificate`, `NewClient` e `SignEnveloped`.

---

## Registro de execução (2026-10-24)

O Claude executou o plano a pedido do dono, que estuda o núcleo `dfe/` pelo guia de estudo. As decisões abaixo mudaram ou completaram o plano. Cada uma diz o que foi decidido, por quê, e o custo se estiver errada.

- **Savepoint por página na varredura.** O plano descartava a rodada inteira quando uma página falhava. Agora cada página grava sob um `SAVEPOINT`, e uma falha mantém as páginas anteriores com o NSU delas. *Por quê:* busca menos de novo e continua sem perder nota, porque documento e NSU andam juntos. *Se estiver errado:* nenhuma nota se perde; no máximo uma página é buscada de novo.
- **cStat diferente de 137, 138 e 656.** O cStat e o motivo ficam registrados na conta, o `ult_nsu` não muda e a próxima varredura é em uma hora. *Por quê:* uma rejeição (CNPJ diferente do certificado, por exemplo) não melhora insistindo. *Se estiver errado:* demora uma hora para tentar de novo.
- **`CienciaSent` e `CienciaFailed`** no lugar de `MarkCiencia(ok, …)`. *Por quê:* fica mais legível. *Se estiver errado:* nada muda no comportamento.
- **`note_events.xml` já na migração 1.02.** A 1.03 não existe. *Por quê:* nada estava publicado ainda. *Se estiver errado:* nada.
- **Validação do contrato com `ExcludeRequestBody`.** O validador confere a rota, os parâmetros e os formatos. O corpo multipart do certificado é lido e limitado pelo handler (1 MB para o arquivo e 2 MB para a requisição inteira). *Por quê:* a validação de multipart do kin-openapi é frágil. *Se estiver errado:* um corpo fora do contrato chega ao handler, que responde 400.
- **IDs no caminho como `string` com `pattern`,** e não `format: uuid`. *Por quê:* `format: uuid` faria o gerador puxar o `google/uuid`, e a convenção é o `uuid` da biblioteca padrão. *Se estiver errado:* nada, porque o padrão é o mesmo.
- **Ciência à mão.** Vale para conta com a Ciência automática desligada (nota em `nao_aplica`) e também para nota em `falhou`, mesmo com a automática ligada. Nota completa ou cancelada responde 409. *Por quê:* é o caso real de uma Ciência que falhou cinco vezes. *Se estiver errado:* basta tirar uma linha do `switch` em `RequestCiencia`.
- **`dfe.ErrInvalidPFX`.** Arquivo que não é PFX vira um erro sentinela, e o handler devolve 422 sem comparar texto. *Por quê:* comparar a mensagem é frágil.
- **Prefixo da chave de API com 12 caracteres** (`fk_` mais 9), com `UNIQUE`. *Por quê:* 8 caracteres deixavam só 5 aleatórios. *Se estiver errado:* nada.
- **C14N própria** (`dfe/c14n.go`) em vez de biblioteca. É conferida contra o `xmllint --c14n`, e a assinatura inteira contra o `xmlsec1`. Os dois testes pulam se a ferramenta não estiver instalada. *Por quê:* o formato da NF-e é fixo, a canonicalização cabe em 250 linhas, e o dono quer entender cada byte. *Se estiver errado:* aparece como 297 na homologação, e o guia de estudo tem o roteiro de depuração.
- **`docker compose` por padrão** no makefile, com `COMPOSE="podman compose"` para o podman. *Por quê:* a máquina de desenvolvimento só tem docker.

### Correções da revisão final (2026-10-24)

Uma revisão independente do repositório inteiro achou problemas que os testes não pegavam. Todos foram corrigidos, com testes novos:

- **A reserva vem antes da SEFAZ.** `account.Store.Claim` grava `next_scan_at = agora + espera` numa transação curta, e só depois o folio chama a SEFAZ. Antes, a hora só era gravada no fim de uma transação longa. Se o processo caísse ou fosse reiniciado no meio, a conta continuava vencida e podia ser consultada de novo em segundos, o que dá risco de 656.
- **Nenhuma transação fica aberta durante uma chamada de rede.** Cada página e cada Ciência gravam numa transação curta, com a trava da conta. Pausar ou pedir uma Ciência à mão não espera mais a varredura.
- **A espera conta a partir da resposta da SEFAZ,** e não do início da rodada, e virou 65 minutos para absorver a diferença de relógio.
- **Um 138 que não avança o NSU para a rodada.** Antes, ele repetia 20 vezes e voltava no tick seguinte, sem fim.
- **`Loop` para até o próximo tick quando `RunOnce` dá erro,** e o `serve` espera a varredura terminar antes de fechar o banco.
- **Novo status `denegada`.** Uma nota denegada não recebe Ciência e não pode ser confundida com uma nota válida.
- **Ciência de nota que ficou inválida.** Um resumo novo dizendo que a nota foi cancelada ou denegada tira a Ciência pendente.
- **Um `idLote` diferente por evento,** feito com milissegundos e um contador.
- **O `retEvento` é copiado como veio,** sem os namespaces do SOAP. Num 573 o folio não guarda um `procEventoNFe` falso.
- **O valor cifrado fica amarrado à conta e ao campo.** O AAD agora é a chave mais `<conta>/pfx` ou `<conta>/password`. Ao abrir o certificado, o folio confere de novo se o CNPJ é o da conta.
- **`VerifyEnveloped` ficou mais rígido.** Ele confere as transformações e exige que a assinatura fique ao lado do elemento assinado. A documentação diz que ele não serve para confiar em documento de terceiros.
- **Detalhes do cliente HTTP.** O transporte TLS ganhou timeouts, aceita a renegociação que alguns servidores IIS da SEFAZ pedem, e as conexões são fechadas no fim de cada rodada.
- **Um `dhResp` inválido não descarta mais a página.**
