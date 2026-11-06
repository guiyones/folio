# Pendências

Última revisão de código: 628111f

| # | Origem | Tipo | Tamanho | Descrição | Estado |
|---|--------|------|---------|-----------|--------|
| 1 | revisão | bug | P | `cmd/folio/serve.go:66` Se a API não sobe (porta ocupada), o processo fica vivo sem API e com a varredura rodando até chegar um sinal: `defer scanning.Wait()` espera um `Loop` que só para com o ctx do sinal. Corrigir com `context.WithCancel` e `defer cancel()` depois do Wait, e teste com a porta ocupada. | feito |
| 2 | revisão | melhoria | P | `internal/scan/scan.go:112` Erro de `connect` volta sem o id da conta, e o log não diz qual conta falhou. Embrulhar com `connecting account[%s]: %w`. | feito |
| 3 | revisão | bug | M | `internal/scan/scan.go:150` `err.Error()` de OpenCertificate, Dial e DistNSU vai para `last_message`, que a API devolve ao cliente (vaza key ID, detalhes de TLS e de URL). Gravar uma mensagem fixa e deixar o erro completo só no log; atualizar TestScanErrorKeepsEarlierPages e TestScanErrorOnFirstPage. | feito |
| 4 | revisão | melhoria | M | `internal/scan/scan_test.go` Nenhum teste cobre `connect` falhando por motivo que não é vencimento (chave rotacionada, Dial com erro). | feito |
| 5 | revisão | melhoria | G | `internal/httpapi/notes.go:94` A regra de quem pode pedir a Ciência, a transação e o lock ficam no handler; `RequestScan` (accounts.go:105) repete a condição de `Claim`. Criar uma operação de negócio com sentinelas, e o handler só traduz o erro. | feito |
| 6 | revisão | melhoria | M | `internal/scan/scan.go:226` A regra "escrita em notas sob o lock da conta" é garantida só por quem chama (4 lugares). Centralizar num `WithLock` ou numa transação travada. | feito |
| 7 | revisão | melhoria | P | `dfe/sign.go:100` Linha reescreve `findDSig(root, "Signature")` à mão; o find por Id aparece duplicado (42-45 e 132-135). | feito |
| 8 | revisão | melhoria | P | `dfe/sign.go:207` `decodeChild` com `local==""` muda de modo; a mensagem sai sem o nome. Separar em `decodeText`. | feito |
| 9 | revisão | melhoria | P | `dfe/ber.go:429` `utf16.Encode` sobra depois de recusar `r > 0xffff`; tirar o import. | feito |
| 10 | revisão | melhoria | P | `dfe/ber.go:124` O `for` com `break` esconde a regra de tentar a senha vazia como nil; deixar as duas tentativas explícitas. | feito |
| 11 | revisão | melhoria | P | `dfe/evento.go:122` O `switch` liga Duplicate e um `if` logo abaixo retorna; `TrimSpace` é calculado 2x. | feito |
| 12 | revisão | melhoria | P | `dfe/c14n.go:176` O helper `has` tem 1 uso e o mapa é consultado 2x. | feito |
| 13 | revisão | melhoria | P | `cmd/folio/admin.go:221` `accountAndClient` tem 1 chamador; chamar `parseID` direto. | feito |
| 14 | revisão | melhoria | P | `cmd/folio/admin_test.go:82` `writeFile` tem 1 uso e só repassa `os.WriteFile`. | feito |
| 15 | revisão | melhoria | P | `dfe/distdfe.go:111` O `if DhResp != ""` sobra; `time.Parse` já falha com vazio. | feito |
| 16 | revisão | melhoria | P | `internal/platform/db/db.go:143` `db.Rollback` não tem chamador; apagar. | feito |
| 17 | revisão | melhoria | P | `internal/apiclient/store.go:49` `apiclient.Store.ByID` não tem chamador; apagar. | feito |
| 18 | revisão | melhoria | M | `internal/account/store.go:327` O padrão "RowsAffected, n==0 → erro" aparece 4x (account, note 2x, apiclient); criar `db.MustAffect(res, none)`. | feito |
| 19 | revisão | melhoria | P | `internal/note/store.go:349` `parseID(s, isNew)` só repassa `isNew`; virar `parseDocID(s)`. | feito |
| 20 | revisão | melhoria | P | `internal/account/lock.go:44` A variável `errConv` é um desvio só para ligar `found`. | feito |
| 21 | revisão | melhoria | P | `internal/httpapi/accounts.go:208` `readCertificateForm` devolve 4 valores com `tooBig bool`; usar o sentinela `errTooBig`. | feito |
| 22 | revisão | melhoria | P | `internal/note/store.go:263` Struct anônima igual a `Event` copiada campo a campo; usar `Event(r)`. | feito |
| 23 | revisão | melhoria | P | `internal/scan/scan.go:304` `id := docID` só para tirar o endereço; usar `&docID`. | feito |
| 24 | revisão | melhoria | P | `internal/apiclient/store.go:74` `plain[:prefixLen]` repete `Prefix()`. | feito |
| 25 | revisão | melhoria | P | `internal/platform/crypt/box.go:58` Valor padrão com `if`; usar `cmp.Or`. | feito |
| 26 | revisão | melhoria | P | `internal/note/note.go:7` O comentário cita `LockDue`, que não existe (o certo é `Claim`/`Lock`). | feito |
| 27 | revisão | melhoria | P | `cmd/folio/consulta.go:78` Erro de `KindOf` descartado sem comentário; o schema real some. | feito |
| 28 | revisão | melhoria | P | `cmd/folio/admin.go:63` (e consulta, serve) `-h` sai como "erro: flag: help requested", com exit 1. | feito |
| 29 | revisão | melhoria | P | `dfe/dfetest/dfetest_test.go:69` `resp.Body.Close()` sem checar e sem comentário. | feito |
| 30 | revisão | melhoria | P | `zarf/compose/docker_compose.yaml:7` Postgres de dev escuta em todas as interfaces com postgres/postgres; publicar só em 127.0.0.1. | aberto |
| 31 | revisão | melhoria | P | `internal/scan/scan.go:351` `errors.Join` em vez de `%w`; a mensagem sai em 2 linhas. | aberto |
| 32 | revisão | melhoria | P | `internal/note/store.go:249` Erro de `KindOf` volta cru, sem operação nem noteID. | aberto |
| 33 | revisão | melhoria | P | `internal/httpapi/accounts.go:234` Senha com mais de 1024 bytes é truncada e vira 422 enganoso; `MaxBytesError` na parte da senha cai em 400. | aberto |
| 34 | revisão | melhoria | P | `internal/platform/db/dbtest/dbtest.go:56` `t.Fatalf` antes do `t.Cleanup` deixa a conexão admin aberta e o banco sem DROP. | aberto |
| 35 | revisão | melhoria | P | Testes de account/note/scan/httpapi sem `t.Parallel()`, embora cada um crie o próprio banco. | aberto |
| 36 | revisão | melhoria | P | `internal/httpapi/httpapi.go:31` O limite de "vence em breve" está duplicado com `scan.CertWarn`; mover para `account`. | aberto |
| 37 | revisão | melhoria | G | `internal` O layout não segue app/business/foundation. Não migrar agora; o primeiro passo é o #5. | aberto |
| 38 | revisão | melhoria | P | `internal/httpapi/accounts.go:105` Sobra do #5: `RequestScan` ainda repete no handler a condição do `Claim` (pausada, certificado, `next_scan_at`). | aberto |
