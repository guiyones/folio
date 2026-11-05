---
projeto: folio
data: 2026-11-05 14:53
tarefa: "Revisão: serve termina a varredura quando a API não sobe"
conceito: "context.WithCancel: um ctx filho que a própria função cancela para parar a goroutine que ela abriu"
tags: [go, context, goroutine]
---
## Contexto
A varredura roda numa goroutine (função que corre em paralelo) e para
quando o seu `ctx` é cancelado. Antes, esse ctx era o que `serve` recebe,
cancelado só por sinal. Se `serve` saísse por outro motivo, ninguém parava
a varredura.

## O código
`cmd/folio/serve.go:68` e `cmd/folio/serve.go:85`
```go
ctx, stopScan := context.WithCancel(ctx)
// ...
scanning.Go(func() { scanner.Loop(ctx, scan.Tick) })
```

## Linha a linha
- `context.WithCancel(ctx)` cria um ctx filho e devolve a função `stopScan` que o cancela.
- O filho é cancelado em dois casos: quando o pai é cancelado (o sinal continua valendo) ou quando alguém chama `stopScan()`.
- `ctx, stopScan := ...` sombreia (substitui no resto da função) o `ctx` antigo: a varredura e o `select` da linha 114 passam a usar o filho.
- `scanning.Go(...)` (método de `sync.WaitGroup` que soma 1, roda a função numa goroutine e marca como feita ao fim) inicia o `Loop`, que recebe o ctx filho.

## Por que assim
Quem abre uma goroutine deve poder fechá-la. Com um cancel próprio, `serve`
não depende de um sinal externo: qualquer `return` chama `stopScan` pelo
`defer` (ver `2026-11-05-1453-defer-lifo.md`). O teste
`cmd/folio/serve_test.go` ocupa a porta e exige que `serve` volte em até
10 s com "address already in use". Como o `Loop` reage ao cancelamento
por dentro está em `internal/scan`, fora desta tarefa.
