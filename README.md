# folio

Motor fiscal próprio, em Go, que fala direto com a SEFAZ e é servido como API.

Nesta primeira parte, o folio busca na SEFAZ (NFeDistribuicaoDFe) as notas emitidas contra o CNPJ de cada conta, faz a Ciência da Operação, guarda os XMLs e entrega as notas por uma API com cursor. A emissão de NFC-e e NF-e vem depois.

- Design: `docs/superpowers/specs/2026-10-23-folio-consulta-design.md`
- Plano: `docs/superpowers/plans/2026-10-23-folio-consulta.md`

## Desenvolvimento

```sh
make dev-up   # Postgres em localhost:5452
make test
make lint
```
