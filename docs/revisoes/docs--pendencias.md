# Revisão guiada: docs/pendencias.md
hash: 5e41b45225491571a9b3582fe999c7b7d0d6756d
atualizado: 2026-11-05 22:45

1. [feito] Se a API não sobe (porta ocupada), o processo fica vivo sem API e com 
2. [feito] Erro de `connect` volta sem o id da conta, e o log não diz qual conta 
3. [feito] `err.Error()` de OpenCertificate, Dial e DistNSU vai para `last_messag
4. [feito] Nenhum teste cobre `connect` falhando por motivo que não é vencimento 
5. [feito] A regra de quem pode pedir a Ciência, a transação e o lock ficam no ha
6. [feito] A regra "escrita em notas sob o lock da conta" é garantida só por quem
7. [feito] Linha reescreve `findDSig(root, "Signature")` à mão; o find por Id apa
8. [feito] `decodeChild` com `local==""` muda de modo; a mensagem sai sem o nome.
9. [feito] `utf16.Encode` sobra depois de recusar `r > 0xffff`; tirar o import. 
10. [feito] O `for` com `break` esconde a regra de tentar a senha vazia como nil; 
11. [feito] O `switch` liga Duplicate e um `if` logo abaixo retorna; `TrimSpace` é
12. [feito] O helper `has` tem 1 uso e o mapa é consultado 2x. 
13. [feito] `accountAndClient` tem 1 chamador; chamar `parseID` direto. 
14. [feito] `writeFile` tem 1 uso e só repassa `os.WriteFile`. 
15. [feito] O `if DhResp != ""` sobra; `time.Parse` já falha com vazio. 
16. [feito] `db.Rollback` não tem chamador; apagar. 
17. [feito] `apiclient.Store.ByID` não tem chamador; apagar. 
18. [feito] O padrão "RowsAffected, n==0 → erro" aparece 4x (account, note 2x, api
19. [feito] `parseID(s, isNew)` só repassa `isNew`; virar `parseDocID(s)`. 
20. [feito] A variável `errConv` é um desvio só para ligar `found`. 
21. [feito] `readCertificateForm` devolve 4 valores com `tooBig bool`; usar o sent
22. [feito] Struct anônima igual a `Event` copiada campo a campo; usar `Event(r)`.
23. [feito] `id := docID` só para tirar o endereço; usar `&docID`. 
24. [feito] `plain[:prefixLen]` repete `Prefix()`. 
25. [feito] Valor padrão com `if`; usar `cmp.Or`. 
26. [feito] O comentário cita `LockDue`, que não existe (o certo é `Claim`/`Lock`)
27. [feito] Erro de `KindOf` descartado sem comentário; o schema real some. 
28. [feito] (e consulta, serve) `-h` sai como "erro: flag: help requested", com ex
29. [feito] `resp.Body.Close()` sem checar e sem comentário. 
30. [feito] Postgres de dev escuta em todas as interfaces com postgres/postgres; p
31. [feito] `errors.Join` em vez de `%w`; a mensagem sai em 2 linhas. 
32. [feito] Erro de `KindOf` volta cru, sem operação nem noteID. 
33. [feito] Senha com mais de 1024 bytes é truncada e vira 422 enganoso; `MaxBytes
34. [feito] `t.Fatalf` antes do `t.Cleanup` deixa a conexão admin aberta e o banco
35. [feito]  Testes de account/note/scan/httpapi sem `t.Parallel()`, embora cada u
36. [feito] O limite de "vence em breve" está duplicado com `scan.CertWarn`; mover
37. [depois] O layout não segue app/business/foundation. Não migrar agora; o primei
38. [depois] Sobra do #5: RequestScan ainda repete no handler a condição do Claim
39. [feito] Upload acima de 2 MB responde 401 em vez de 413
40. [depois] httpapi_test.go:143 Body.Close sem comentário
