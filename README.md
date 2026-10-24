# folio

Motor fiscal próprio, em Go, que fala direto com a SEFAZ e é servido como API.

Nesta primeira parte, o folio busca na SEFAZ (NFeDistribuicaoDFe) as notas emitidas contra o CNPJ de cada conta, faz a Ciência da Operação, guarda os XMLs e entrega as notas por uma API com cursor. A emissão de NFC-e e NF-e vem depois.

- Design: `docs/superpowers/specs/2026-10-23-folio-consulta-design.md`
- Plano: `docs/superpowers/plans/2026-10-23-folio-consulta.md`
- API: `internal/httpapi/openapi.yaml` (o contrato; o código em `internal/httpapi/gen` é gerado dele)

## Organização

| Pasta | O que tem |
| --- | --- |
| `dfe/` | a biblioteca do motor: certificado A1, mTLS, SOAP, NFeDistribuicaoDFe, leitura dos documentos, assinatura XML-DSig e o evento de Ciência. Não conhece banco nem HTTP de entrada. |
| `dfe/dfetest/` | certificado de teste e SEFAZ falsa com mTLS |
| `internal/account/` | contas, certificado cifrado e a trava da varredura |
| `internal/apiclient/` | clientes da API e as chaves |
| `internal/note/` | documentos recebidos, notas, eventos e o cursor de mudanças |
| `internal/scan/` | o laço de varredura e a Ciência automática |
| `internal/httpapi/` | o contrato OpenAPI e os handlers |
| `internal/platform/` | banco (`db`) e cifra (`crypt`) |
| `cmd/folio/` | o binário: `serve`, `admin` e `consulta` |

## Variáveis de ambiente

| Variável | Para quê |
| --- | --- |
| `FOLIO_DB_URL` | Postgres, por exemplo `postgres://postgres:postgres@localhost:5452/folio?sslmode=disable` |
| `FOLIO_CERT_KEY` | chave de 32 bytes em base64 que cifra os certificados. Gere com `openssl rand -base64 32` e guarde fora do banco. Perder a chave = perder os certificados guardados. |
| `FOLIO_CERT_KEY_ID` | nome da chave (padrão `k1`), para trocar de chave no futuro |
| `FOLIO_ADDR` | endereço da API (padrão `:8080`) |
| `FOLIO_SEFAZ_CA` | PEM extra com a cadeia da SEFAZ, se o sistema não confiar nela. Nunca desligue a verificação TLS. |
| `FOLIO_PFX_PASSWORD` | senha do certificado para `admin cert-upload` e `consulta`; sem ela, a senha é pedida no terminal. Nunca vai por argumento. |

## Uso

```sh
make dev-up                                   # Postgres em localhost:5452
export FOLIO_DB_URL=postgres://postgres:postgres@localhost:5452/folio?sslmode=disable
export FOLIO_CERT_KEY=$(openssl rand -base64 32)   # guarde este valor

go run ./cmd/folio admin migrate
go run ./cmd/folio admin client-create --name cadim             # imprime o ID do cliente
go run ./cmd/folio admin key-create --client <cliente>          # a chave aparece uma vez só
go run ./cmd/folio admin account-create --client <cliente> --name Franquia --cnpj 11222333000181 --uf 41 --env 2
go run ./cmd/folio admin cert-upload --account <conta> --pfx certificado.pfx

go run ./cmd/folio serve
curl -H "Authorization: Bearer fk_..." localhost:8080/v1/accounts/<conta>/notes
```

Para acompanhar as notas, guarde o `next_after` de cada página e passe como `after` na próxima. Uma nota volta a aparecer sempre que muda: quando o XML completo chega, quando é cancelada, quando a Ciência é feita.

### Prova de conceito sem banco

```sh
go run ./cmd/folio consulta --pfx certificado.pfx --uf 41 --amb 2
```

Consulta a SEFAZ uma vez, mostra os documentos e grava os XMLs em `out/`. Em produção (`--amb 1`), rode com qualquer outra ferramenta que consulte o mesmo CNPJ pausada: duas consultas na mesma hora podem levar a SEFAZ a bloquear o CNPJ por consumo indevido (cStat 656).

## Desenvolvimento

```sh
make dev-up     # Postgres em localhost:5452 (COMPOSE="podman compose" para usar o podman)
make generate   # regenera internal/httpapi/gen a partir do openapi.yaml
make test       # os testes de banco usam FOLIO_TEST_DB_URL, que o makefile define
make lint
```

Os testes de assinatura conferem o resultado com o `xmllint` e o `xmlsec1` quando eles estão instalados.
