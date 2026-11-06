---
projeto: folio
data: 2026-11-05 21:52
tarefa: "Revisão: escrita em notas exige a conta travada; regra da Ciência sai do handler"
conceito: "Regra de negócio no pacote de domínio com erros sentinela; o handler HTTP só traduz o erro"
tags: [go, erros, arquitetura]
---
## Contexto
Quem pode pedir a Ciência à mão era decidido num `switch` no handler HTTP,
com as mensagens escritas ali. A regra foi para `note.CanRequestCiencia`, uma
função pura (sem banco), testada em `internal/note/note_test.go`.

## O código
`internal/note/ciencia.go:60-71`
```go
func CanRequestCiencia(n Note, autoCiencia bool) error {
	switch {
	case n.Ciencia == CienciaFailed:
		return nil
	case n.Ciencia == CienciaNotApplicable && !autoCiencia:
		return nil
	case n.Ciencia == CienciaNotApplicable:
		return ErrAutoCiencia
	default:
		return fmt.Errorf("%w %s", ErrCienciaState, n.Ciencia)
	}
}
```

## Linha a linha
- `switch {` sem valor testa cada `case` como um `if`, de cima para baixo; o primeiro verdadeiro ganha.
- `nil` quer dizer "pode pedir": falhou antes, ou a conta não faz a Ciência sozinha.
- `ErrAutoCiencia` e `ErrCienciaState` são sentinelas (erros fixos declarados em `internal/note/note.go:23-24`, comparáveis com `errors.Is`).
- `%w` embrulha a sentinela e acrescenta o estado atual: a mensagem fica "a Ciência desta nota já está feita" e `errors.Is(err, ErrCienciaState)` continua verdadeiro.
- No handler (`internal/httpapi/notes.go:107-109`), sobra só: se deu erro, responde 409 com `err.Error()`.

## Por que assim
No handler, só um teste HTTP exercitava a regra, e outra entrada teria que
copiá-la. Em `note`, ela tem dono e teste de tabela; o handler cuida só de
HTTP. `RequestScan` ainda repete regra no handler (achado 38), fora daqui.
