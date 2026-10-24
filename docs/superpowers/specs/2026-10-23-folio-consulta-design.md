# folio, parte de consulta: design

Data: 2026-10-23 · Repo: github.com/guiyones/folio (novo) · Quem escreve o código: o próprio dono do produto, com o Claude guiando e revisando.

## Intenção

O folio é um motor fiscal próprio, escrito em Go, que fala direto com a SEFAZ, sem provedor no meio (Focus NFe, NFE.io e afins), e que é servido como uma API. Ele é um produto separado. O Cadim (mercadinhos autônomos) é o primeiro cliente, o beep (fábrica de esfihas) vem depois, e outros podem vir.

O folio terá duas frentes:

1. **Consulta:** buscar na SEFAZ as notas emitidas contra o CNPJ de um cliente.
2. **Emissão:** NFC-e (cupom fiscal) e NF-e 55.

Este documento cobre só a **consulta**. A emissão terá um design próprio, que aproveita o que for construído aqui (certificado, assinatura XML, conexão com a SEFAZ).

### Por que a consulta vem primeiro

O Leandro, sócio de uma franquia de mercadinhos autônomos (InHouse Market), compra tudo para um estoque central e quer que as notas emitidas contra o CNPJ dele cheguem sozinhas no Cadim. Hoje ele roda no Windows um script em Python, feito por ele, que:

- consulta a SEFAZ (NFeDistribuicaoDFe) de hora em hora a partir do último NSU;
- separa os resumos, as notas completas e os eventos;
- faz a Ciência da Operação para liberar o XML completo;
- casa os itens com o catálogo dele e gera um XML de importação.

O folio substitui a parte de SEFAZ desse script como serviço. O casamento de produtos vai para o Cadim, num projeto à parte.

Hoje, quem emite o cupom fiscal nas vendas é o sistema da franqueadora. Por isso a emissão não tem pressa e fica para depois. O dono do produto vai confirmar isso com o Leandro.

### Base

A análise anterior, feita para o beep, vale como base:

- `risecode/playbooks/beep/specs/2026-10-05-nfe-levantamento.md` decidiu emitir direto na SEFAZ, com uma biblioteca própria, independente do sistema que a usa (dados entram, XML e resposta saem) e com várias contas desde o primeiro dia.
- A spec de 2026-08-11 do beep descreve a máquina de estados, o reenvio com consulta de status antes e o arquivo dos XMLs por 5 anos.

Aquelas decisões foram tomadas pensando em Rails. Aqui elas viram Go, e o motor vira um serviço.

Também serve de referência o script do Leandro, em `~/Downloads/resumo-correcao-xml-sefaz-nfe/sefaz-nfe-integracao/src/`, especialmente `sefaz_client.py`, `manifestacao.py` e `nsu_state.py`. É material do cliente, então só pode ser usado como referência e não pode ir para o repositório.

## Objetivos

- **Contas:** cadastrar contas, cada uma com CNPJ, UF, ambiente (produção ou homologação) e certificado A1.
- **Certificado:** receber o certificado A1 (.pfx com senha), conferir se ele é do CNPJ da conta e guardá-lo cifrado.
- **Varredura:** buscar os documentos novos de cada conta na NFeDistribuicaoDFe, a partir do último NSU, sem levar bloqueio por consumo indevido.
- **Ciência automática:** quando chegar só o resumo de uma nota, enviar a Ciência da Operação para liberar o XML completo.
- **Arquivo:** guardar os XMLs, tanto o resumo quanto a nota completa e os eventos, pelo menos pelos 5 anos que a lei pede.
- **Entrega:** entregar ao cliente da API, pela própria API, as notas que mudaram desde a última vez que ele perguntou, com o XML completo.
- **Aviso de vencimento:** avisar que o certificado está perto de vencer.

## Não objetivos (agora)

- Emissão de NFC-e e NF-e (design próprio, depois).
- Manifestações conclusivas: Confirmação da Operação, Desconhecimento e Operação não Realizada. Só a Ciência entra agora.
- Casamento de produtos, estoque, categoria financeira e DRE. Isso é do Cadim.
- Avisar o cliente por webhook quando chegar nota. O cliente pergunta e, se precisar, isso vem depois.
- Painel web. A administração é pela API e pelo terminal.
- NFS-e e CT-e.
- Cobrança e cadastro de clientes por conta própria (self-service).

## Anti-objetivos (seria falha mesmo funcionando)

- **Levar bloqueio da SEFAZ por consumo indevido** (cStat 656). Se o certificado de um cliente é bloqueado, a consulta dele para. Respeitar os limites é requisito, não detalhe.
- **Vazar certificado ou senha**, seja em log, numa resposta da API, numa mensagem de erro, no banco sem cifra ou num commit.
- **Perder uma nota.** Se o NSU avançar sem que o documento tenha sido gravado, a nota não volta mais. Gravar o documento e avançar o NSU acontecem juntos, ou nenhum dos dois acontece.
- **Uma conta enxergar a nota de outra.** Cada chave de API só vê as contas dela.
- **Descartar erro.** Todo erro é embrulhado com contexto, mapeado para uma resposta clara ou registrado. Um retorno ignorado de propósito leva um comentário dizendo por quê.
- **Ficar preso a um cliente.** Nada de Cadim ou de mercadinho dentro do folio.

## Restrições

- **Linguagem e banco:** Go e Postgres.
- **Repositório:** github.com/guiyones/folio.
- **Quem escreve:** o dono do produto escreve o código. O design e o plano precisam ser legíveis para quem vai digitar, com passos pequenos e motivo explicado.
- **SEFAZ:**
  - A NFeDistribuicaoDFe é do Ambiente Nacional. Ela usa SOAP 1.2 com certificado do cliente na conexão (mTLS).
  - Produção: `https://www1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx`.
  - Homologação: `https://hom1.nfe.fazenda.gov.br/NFeDistribuicaoDFe/NFeDistribuicaoDFe.asmx`.
  - Esses endereços foram tirados do script do cliente e precisam ser conferidos no Portal da NF-e.
  - A Ciência da Operação (evento 210210) vai para o RecepcaoEvento do Ambiente Nacional (cOrgao 91), assinada em XML-DSig.
- **Homologação não tem notas reais.** Em homologação, a consulta prova a conexão, o SOAP e o tratamento das respostas, mas deve responder "nenhum documento" (cStat 137). A prova com notas de verdade é em produção, com o certificado do Leandro, só para consulta, o que não gera efeito fiscal. A Ciência em produção é um evento real, mas é o mesmo que o script dele já faz.
- **Limite de consulta:**
  - Depois de uma resposta "nenhum documento" (137) ou de "consumo indevido" (656), aquela conta só pode ser consultada de novo uma hora depois.
  - Enquanto a resposta trouxer documentos e o `ultNSU` for menor que o `maxNSU`, pode continuar buscando na sequência, porque é assim que a SEFAZ manda paginar.
- **Retenção:** os XMLs ficam guardados por pelo menos 5 anos.

## Como funciona

### Contas e certificado

- **Conta:** um CNPJ que o folio consulta. Tem nome, CNPJ, UF (o `cUFAutor`, que no PR é 41), ambiente, último NSU, horário da próxima varredura permitida, último cStat com a mensagem e o horário da última varredura.
- **Uma conta por CNPJ e ambiente.** O NSU e o limite de uma consulta por hora são do CNPJ na SEFAZ, não de uma conta do folio. Por isso o mesmo CNPJ só pode ser cadastrado uma vez em cada ambiente. Se dois clientes da API precisarem das notas do mesmo CNPJ (Cadim e beep, por exemplo), a conta é uma só, ligada aos dois.
- **Quem cria a conta:** o dono do produto, pelo `folio admin`, ligando a conta a um ou mais clientes da API. Assim ninguém descobre pela API que um CNPJ já está cadastrado por outro cliente.
- **NSU inicial:** ao criar a conta, dá para informar o NSU de partida. O padrão é 0, que traz tudo que a SEFAZ ainda guarda.
- **Pausa:** a conta pode ser pausada e retomada (pelo `admin` e pela API). Uma conta pausada não é varrida nem envia Ciência, mas continua com as notas disponíveis para leitura.
- **Sem exclusão nesta etapa.** Uma conta que deixa de ser usada fica pausada. O certificado dela pode ser apagado (pelo `admin`), mas os XMLs ficam pelos 5 anos. Exportar o arquivo para o cliente que sai fica para depois.
- **Envio do certificado:**
  - O certificado chega pela API (o .pfx e a senha) e o folio abre o arquivo para conferir.
  - O CNPJ do certificado tem que ser o da conta. Se não for, o envio é recusado com uma mensagem clara.
  - O folio extrai a validade e o titular.
- **Guarda do certificado:**
  - O .pfx e a senha são gravados cifrados com AES-256-GCM.
  - A chave de cifra vem de uma variável de ambiente (sugestão: `FOLIO_CERT_KEY`, 32 bytes em base64) e nunca vai para o banco.
  - Cada registro guarda um identificador da chave usada, para dar para trocar de chave no futuro.
- **O que a API mostra:** titular, CNPJ, validade e quando o certificado foi enviado. Nunca o arquivo nem a senha.
- **Troca:** enviar um certificado novo substitui o anterior. Isso acontece todo ano, porque o A1 vale um ano.
- **Vencimento:**
  - Faltando 30 dias ou menos, a conta aparece com aviso na API e no log.
  - Com o certificado vencido, a varredura daquela conta para e registra o motivo.
  - O aviso por e-mail fica para depois.

### Varredura

- **Quem roda:** um laço dentro do próprio processo do folio. A cada poucos minutos, ele pega as contas cujo horário de próxima varredura já passou.
- **Uma conta por vez:** cada conta é travada no banco enquanto está sendo varrida (`FOR UPDATE SKIP LOCKED` ou equivalente). Assim, duas instâncias nunca consultam a mesma conta ao mesmo tempo.
- **Cada chamada:**
  - Envia `distNSU` com o `ultNSU` da conta.
  - A resposta traz o cStat, o `ultNSU`, o `maxNSU` e até 50 documentos (`docZip`, em base64 e gzip).
  - Para cada documento, o folio grava o NSU, o schema (`resNFe`, `procNFe`, `resEvento`, `procEventoNFe`) e o XML.
  - Na **mesma transação**, o folio avança o `ultNSU` da conta.
- **Depois da chamada:**
  - Se o `ultNSU` for menor que o `maxNSU`, chama de novo na hora.
  - Se não, ou se vier 137, a próxima varredura fica para daqui a uma hora.
  - Se vier 656, também fica para daqui a uma hora, e o folio registra um alerta.
  - Erro de rede ou resposta inesperada também adia em uma hora: errar para menos consultas é melhor que levar bloqueio. O erro fica gravado na conta.
- **Varredura pedida:** o cliente da API pode pedir uma varredura agora, como o botão "Rodar varredura agora" do script do Leandro. O pedido respeita o limite: se ainda não passou uma hora, a API responde com o horário em que vai poder rodar.

### Documentos

O que o cliente da API enxerga é a **nota**, identificada pela chave de acesso de 44 dígitos. Os documentos brutos que chegam com cada NSU continuam guardados, mas por baixo.

- **Resumo:** quando chega um `resNFe`, nasce a nota com situação "resumo". Ela já traz emitente (CNPJ e nome), valor, data de emissão e situação na SEFAZ.
- **Nota completa:** quando chega o `procNFe` da mesma chave, a nota passa para a situação "completa" e ganha o XML completo.
- **Eventos:** um evento da nota, como o cancelamento pelo emitente ou a Ciência que o folio enviou, fica ligado a ela. O cancelamento muda a situação da nota para "cancelada".
- **Cursor de mudanças:**
  - Toda vez que uma nota nasce ou muda, ela recebe um número novo de uma sequência do próprio folio.
  - É esse número que o cliente usa como cursor ("o que mudou depois do 1234"). O NSU não serve como cursor, porque a mesma nota aparece em mais de um NSU.
  - Assim o Cadim vê a nota de novo quando ela fica completa ou é cancelada.

### Ciência da Operação

- Ao gravar um `resNFe` cuja nota ainda não tem a Ciência, o folio monta o evento 210210, assina em XML-DSig com o certificado da conta e envia ao RecepcaoEvento do Ambiente Nacional.
- O resultado vira um evento ligado à nota.
- Se falhar, a nota fica marcada para tentar de novo, com limite de tentativas. Ela não trava a varredura das outras.
- Depois da Ciência aceita, o XML completo chega numa das próximas varreduras, com outro NSU.
- A Ciência automática é uma opção da conta, ligada por padrão. Uma conta pode desligar e decidir à mão pela API.
- **Notas antigas:** a conta tem uma data de corte. Só recebem Ciência automática os resumos de notas emitidas depois dela. Assim, começar do NSU 0 não dispara Ciência em produção para todo o histórico. O padrão da data de corte é o dia em que a conta foi criada.
- Se a SEFAZ recusar a Ciência porque ela já foi feita (por exemplo, pelo script antigo do cliente), isso conta como sucesso, e não como erro.

### API

A API é HTTP com JSON e fica sob `/v1`.

- **Autenticação:**
  - Cada cliente da API (Cadim, beep) tem uma ou mais chaves.
  - O folio guarda só o hash da chave.
  - Cada chave enxerga só as contas ligadas ao cliente dela.
  - Uma conta de outro cliente responde como inexistente (404), para não revelar que existe.
- **Recursos mínimos:**
  - Ver e listar as contas do cliente (criar é só pelo `admin`). Ao ver uma conta, aparecem o estado da varredura e o aviso de certificado.
  - Pausar e retomar uma conta.
  - Pedir a Ciência de uma nota à mão, para contas com a Ciência automática desligada.
  - Enviar o certificado de uma conta.
  - Pedir uma varredura agora.
  - Listar as notas de uma conta que mudaram depois de um cursor, com limite por página. Cada nota traz chave, emitente, valor, data, situação, cursor e se o XML completo já está disponível.
  - Baixar o XML de uma nota: o completo se houver, senão o resumo.
  - Listar os eventos de uma nota.
- **Administração:** criar clientes da API e as chaves deles é coisa do dono do produto, feita por um comando de terminal (`folio admin ...`), não pela API.

### Comando de terminal

O mesmo binário tem três modos:

- `serve`: sobe a API e a varredura.
- `admin`: cria clientes da API e chaves.
- `consulta`: modo de prova. Recebe o caminho de um .pfx, a UF, o ambiente e um NSU inicial, consulta a SEFAZ uma vez, mostra os documentos e grava os XMLs numa pasta. Não usa banco.

A senha do certificado vem por variável de ambiente ou por pergunta no terminal, nunca por argumento, porque argumento fica no histórico do shell.

## Ordem de construção

Cada etapa prova uma coisa antes da próxima.

1. **Prova de conceito (`folio consulta`).**
   - Abrir o .pfx, montar a conexão com mTLS, montar o SOAP do `distDFeInt`, ler a resposta e descompactar os `docZip`.
   - Prova em homologação: a resposta é um 137, e o XML de resposta é lido sem erro.
   - Prova em produção, com o certificado do Leandro: aparecem as notas dele.
2. **Banco e varredura.** Contas, certificado cifrado, documentos, notas, cursor, laço de varredura com limite e trava por conta.
3. **Ciência da Operação.** Assinatura XML-DSig, evento 210210, tentativas de novo e chegada do XML completo.
4. **API.** Clientes, chaves, os recursos acima e o comando `admin`.

A etapa 3 é a primeira com assinatura XML. É a mesma assinatura que a emissão vai usar, então vale fazer com calma e com testes contra um XML assinado conhecido.

## Decisões que ficam para quem constrói

- Organização dos pacotes e se o núcleo SEFAZ é uma biblioteca importável por outros programas. A recomendação é que seja, como decidido no levantamento do beep.
- Roteador HTTP, biblioteca de migrations, de log e de banco. Seguir o jeito do Cadim (sqlx, SQL puro, CAST em vez de `::`) é bem-vindo, mas não obrigatório.
- Como abrir o .pfx. O `golang.org/x/crypto/pkcs12` não lê bem os .pfx modernos, e o `software.sslmate.com/src/go-pkcs12` é a alternativa conhecida. Conferir na hora.
- Como assinar em XML-DSig: usar uma biblioteca, como `github.com/russellhaering/goxmldsig`, ou escrever a canonicalização para o formato fixo da NF-e. O que vale é o teste contra um XML assinado de referência.
- Onde o folio roda e como é publicado.
- Formato exato do JSON e dos códigos de erro da API.

## Escolhas feitas pelo Claude (o dono pode trocar)

- AES-256-GCM com a chave numa variável de ambiente para guardar o certificado.
- Aviso de vencimento com 30 dias de antecedência.
- O cliente da API pergunta o que mudou (cursor próprio do folio) em vez de receber webhook.
- Ciência da Operação automática, ligada por padrão e que pode ser desligada por conta.
- Erro de rede também adia a próxima varredura em uma hora.
- Conta de outro cliente responde 404.
- Uma conta por CNPJ e ambiente, que pode ser ligada a vários clientes, e criada só pelo `admin`.
- Um certificado por conta, com o mesmo CNPJ dela. Um certificado de matriz servindo filiais fica para quando houver um caso real.
- Conta pausada em vez de excluída, e XMLs mantidos por 5 anos mesmo depois da pausa.
- Data de corte para a Ciência automática, igual ao dia da criação da conta.
- Varredura dentro do mesmo processo da API, com trava por conta no banco.
- XMLs guardados no próprio Postgres. Uma nota tem poucas dezenas de KB, então isso cabe por anos.

## Entrada em produção para o Leandro

- **Um CNPJ:** por enquanto o folio consulta um CNPJ só, o da matriz da franquia. Não há filiais nesta etapa.
- **Sem consulta dupla:** quando o folio começar a consultar esse CNPJ em produção, o script em Python dele para de consultar a SEFAZ. Os dois nunca consultam ao mesmo tempo.
- **Quando entra em produção:** como o script ainda gera o XML de importação dele, o folio só começa a varrer em produção quando o Cadim estiver pronto para receber as notas. Isso é decisão do Claude, a partir da resposta do dono.
- **Antes disso,** o folio é provado:
  - em homologação;
  - em produção, com o `folio consulta`, que roda uma vez só. A rodada é feita longe da rodada de hora em hora do script, e com o script pausado.
- **Ponto de partida:** a conta começa do NSU 0, para o Cadim receber o histórico que a SEFAZ ainda guarda, e a data de corte é o dia da troca. Assim o histórico chega, mas não recebe Ciência de novo. Essa também é uma decisão do Claude.

## Fatos a confirmar na documentação da SEFAZ

- Os endereços de produção e homologação, no Portal da NF-e.
- Os códigos de cStat: 137 (nenhum documento), 138 (documentos encontrados), 656 (consumo indevido), e o comportamento esperado em cada um.
- Por quanto tempo um NSU fica disponível para consulta. A expectativa é de uns 90 dias, e isso define o que acontece se uma conta ficar muito tempo sem varrer.
- O prazo para enviar a Ciência depois da emissão da nota.
- Se um certificado de matriz consegue consultar as notas de uma filial, ou se cada CNPJ precisa do próprio certificado. Isso só importa quando houver filial.

## Decisões já tomadas para o design da emissão

- **Divisão dos impostos:**
  - O sistema cliente (Cadim, beep) decide a tributação de cada item: NCM, CFOP, CSOSN ou CST, CEST e alíquotas.
  - O folio faz a conta: base × alíquota, arredondamento, soma dos itens, totais e rateio de desconto e frete.
  - O folio não guarda regras tributárias por estado.
  - **Por quê:** a SEFAZ confere a conta. Se cada cliente calculasse, o mesmo arredondamento teria que existir em Go e em Ruby, e uma diferença de centavo vira rejeição.
- **Fila:** a emissão é assíncrona. A API responde 202, um worker processa e a fila é o próprio Postgres (`FOR UPDATE SKIP LOCKED`), sem RabbitMQ nem Redis.
- **Estrutura do XML:** structs escritas à mão, só com os campos usados, em vez de structs geradas de todo o XSD.
