---
projeto: folio
data: 2026-11-05 21:52
tarefa: "Revisão: escrita em notas exige a conta travada; regra da Ciência sai do handler"
conceito: "Tipo como garantia: LockedTx só nasce em WithLock, então escrever sem o lock não compila"
tags: [go, tipos, transacao]
---
## Contexto
Toda escrita em notas precisa travar a conta antes, para o cursor de mudanças
sair em ordem de commit. Antes, cada um dos 4 chamadores lembrava de chamar
`Lock` dentro do próprio `db.WithTx`. Agora os métodos de escrita pedem um
`account.LockedTx`, e só `WithLock` (linha 71) sabe criar um.

## O código
`internal/account/lock.go:59-66`
```go
type LockedTx struct {
	*lockedTx
	Account Account // the account as read under the lock
}

// lockedTx keeps the transaction field unexported: code outside this
// package uses its methods but cannot build a LockedTx around any tx.
type lockedTx = sqlx.Tx
```

## Linha a linha
- `*lockedTx` sem nome é um campo embutido (embedding: os métodos do tipo de dentro sobem para o de fora). Por isso `tx.ExecContext(...)` funciona direto num `LockedTx`.
- O nome do campo embutido é o nome do tipo, `lockedTx`, com minúscula: em Go, minúscula é não exportado (invisível fora do pacote). Fora de `account`, ninguém escreve `LockedTx{lockedTx: minhaTx}`.
- `type lockedTx = sqlx.Tx` é um alias (outro nome para o mesmo tipo, não um tipo novo). Serve só para dar ao campo um nome minúsculo; os métodos continuam os de `sqlx.Tx`.
- `Account` vai junto: quem recebe o tx já tem a conta lida sob o lock, e `FinishScan` e `apply` deixaram de receber o id ou a conta à parte.
- Na linha 83, `WithLock` monta `LockedTx{lockedTx: tx, Account: a}` depois do `SELECT ... FOR UPDATE` (trava a linha até o fim da transação).

## Por que assim
A alternativa era manter `*sqlx.Tx` nos métodos e confiar num comentário.
Com o tipo, esquecer o lock vira erro de compilação. Limite honesto: de fora
ainda dá para escrever `account.LockedTx{}` vazio; compila, mas o tx é nil e
quebra na primeira query. A garantia é não conseguir embrulhar uma
transação qualquer. Commit e rollback ficam em `db.WithTx`, fora desta tarefa.
