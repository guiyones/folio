# Revisão guiada: docs/pendencias.md
hash: 5e41b45225491571a9b3582fe999c7b7d0d6756d
atualizado: 2026-11-05 14:50

1. [pendente] Se a API não sobe (porta ocupada), o processo fica vivo sem API e com 
2. [pendente] Erro de `connect` volta sem o id da conta, e o log não diz qual conta 
3. [pendente] `err.Error()` de OpenCertificate, Dial e DistNSU vai para `last_messag
4. [pendente] Nenhum teste cobre `connect` falhando por motivo que não é vencimento 
5. [pendente] A regra de quem pode pedir a Ciência, a transação e o lock ficam no ha
6. [pendente] A regra "escrita em notas sob o lock da conta" é garantida só por quem
7. [pendente] Linha reescreve `findDSig(root, "Signature")` à mão; o find por Id apa
8. [pendente] `decodeChild` com `local==""` muda de modo; a mensagem sai sem o nome.
9. [pendente] `utf16.Encode` sobra depois de recusar `r > 0xffff`; tirar o import. 
10. [pendente] O `for` com `break` esconde a regra de tentar a senha vazia como nil; 
11. [pendente] O `switch` liga Duplicate e um `if` logo abaixo retorna; `TrimSpace` é
12. [pendente] O helper `has` tem 1 uso e o mapa é consultado 2x. 
13. [pendente] `accountAndClient` tem 1 chamador; chamar `parseID` direto. 
14. [pendente] `writeFile` tem 1 uso e só repassa `os.WriteFile`. 
15. [pendente] O `if DhResp != ""` sobra; `time.Parse` já falha com vazio. 
16. [pendente] `db.Rollback` não tem chamador; apagar. 
17. [pendente] `apiclient.Store.ByID` não tem chamador; apagar. 
18. [pendente] O padrão "RowsAffected, n==0 → erro" aparece 4x (account, note 2x, api
19. [pendente] `parseID(s, isNew)` só repassa `isNew`; virar `parseDocID(s)`. 
20. [pendente] A variável `errConv` é um desvio só para ligar `found`. 
21. [pendente] `readCertificateForm` devolve 4 valores com `tooBig bool`; usar o sent
22. [pendente] Struct anônima igual a `Event` copiada campo a campo; usar `Event(r)`.
23. [pendente] `id := docID` só para tirar o endereço; usar `&docID`. 
24. [pendente] `plain[:prefixLen]` repete `Prefix()`. 
25. [pendente] Valor padrão com `if`; usar `cmp.Or`. 
26. [pendente] O comentário cita `LockDue`, que não existe (o certo é `Claim`/`Lock`)
27. [pendente] Erro de `KindOf` descartado sem comentário; o schema real some. 
28. [pendente] (e consulta, serve) `-h` sai como "erro: flag: help requested", com ex
29. [pendente] `resp.Body.Close()` sem checar e sem comentário. 
30. [pendente] Postgres de dev escuta em todas as interfaces com postgres/postgres; p
31. [pendente] `errors.Join` em vez de `%w`; a mensagem sai em 2 linhas. 
32. [pendente] Erro de `KindOf` volta cru, sem operação nem noteID. 
33. [pendente] Senha com mais de 1024 bytes é truncada e vira 422 enganoso; `MaxBytes
34. [pendente] `t.Fatalf` antes do `t.Cleanup` deixa a conexão admin aberta e o banco
35. [pendente]  Testes de account/note/scan/httpapi sem `t.Parallel()`, embora cada u
36. [pendente] O limite de "vence em breve" está duplicado com `scan.CertWarn`; mover
37. [pendente] O layout não segue app/business/foundation. Não migrar agora; o primei
