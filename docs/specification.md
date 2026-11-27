# Especificação da dinâmica — Jogo de festa "impostor" (PWA)

> Nome do jogo: a definir. Inspirado em jogos de festa do tipo "quem está fora do loop", mas com salas online, um celular por jogador e conteúdo próprio.

**Status:** todas as decisões abaixo estão confirmadas. As sugestões feitas durante o planejamento foram aceitas, exceto a exclusão de jogadores da lista de possíveis temas, que foi descartada.

---

## 1. Visão geral

- Jogo de festa **presencial**: as pessoas estão no mesmo lugar, cada uma com o próprio celular.
- Todos recebem uma palavra secreta (ou um nome, no tema "Pessoas da sala"), **menos um jogador, o impostor**.
- As perguntas são **lidas e respondidas em voz alta**. O app não recebe respostas digitadas.
- No fim da rodada, todos votam em quem acham que é o impostor.
- Vence a partida quem somar mais pontos ao final das rodadas.

## 2. Plataforma e tecnologia

- **Front-end:** React, como PWA focado em celular.
- **Back-end:** Go com PostgreSQL.
- **Idioma:** somente português (pt-BR) no início.
- **Contas:** sem cadastro. A pessoa entra com um nome.

## 3. Salas

- Qualquer pessoa cria uma sala e se torna o **anfitrião**.
- Cada sala tem um **código** para entrada. Quem entra informa o código e o seu nome.
- **Foto (opcional):** ao criar ou entrar na sala, o jogador pode escolher uma foto sua (câmera ou galeria). Ela aparece como miniatura em todas as telas e na revelação do impostor. A primeira foto pode ser enviada a qualquer momento; **trocar ou remover só no lobby**. A foto é apagada junto com a sala.
- **Jogadores por sala:** mínimo de **3**, máximo de **20**. Ocupa vaga quem já abriu a sala (conexão em tempo real) ou entrou há menos de 2 minutos: entradas que nunca abrem a sala não a deixam cheia.
- **Entrada:** novos jogadores só podem entrar no **lobby** e **entre rodadas**.
- **Retenção dos dados:** a sala é apagada **1 hora depois de ficar vazia**, ou **24 horas depois de criada**, mesmo que ainda tenha pessoas.
- Código curto (4 a 5 caracteres), sem letras ambíguas (O/0, I/1), com checagem de colisão.
- Nome repetido recebe um número automaticamente. O nome é normalizado (a mesma grafia para acentos compostos ou decompostos) e não aceita caracteres invisíveis ou em branco (como o preenchimento coreano U+3164 ou o braille vazio U+2800), nem pilhas de acentos.
- Se o anfitrião sair ou ficar offline, a função passa para o jogador mais antigo na sala.
- **Saída:** o jogador pode **sair da sala**, e o anfitrião pode **remover** alguém no lobby; os dois pedem **confirmação**. Quem fica **offline por 3 minutos** é retirado da sala. Quem saiu some do lobby, do placar e do pódio e, para voltar, entra de novo como jogador novo.
- Reconexão: o jogador volta à mesma sala e ao mesmo lugar (token guardado no aparelho), importante porque o iOS suspende conexões em segundo plano.

## 4. Configuração da partida

Definida pelo anfitrião ao **criar a sala**:

- **Número de rodadas:** 3, 5, 8, 10 ou 12.
- **Tempo padrão por turno de pergunta:** 3 minutos.

O **tema** não é definido na criação: é escolhido a cada rodada (ver seção 7).

## 5. Fluxo de uma rodada

1. **Escolha do tema** pelo anfitrião.
2. **Revelação do papel** em cada celular.
3. **Perguntas em roda** (seção 8), com histórico (seção 9).
4. **Discussão** livre, com cronômetro de 3 minutos.
5. **Votação.**
6. **Palpite** do impostor, só se ele for descoberto.
7. **Resultado** e **placar** parcial. O anfitrião toca em **"Próxima rodada"** para começar a seguinte; depois da última rodada o botão vira **"Ver pódio"**.

### Revelação do papel
- O papel (palavra ou "você é o impostor") aparece apenas enquanto a pessoa **mantém o botão pressionado**, para ninguém espiar.
- Depois de ver, cada jogador toca em **"Entendi"**. As perguntas começam quando todos os jogadores **conectados** da rodada confirmarem; quem está offline não trava a rodada.
- O anfitrião pode começar as perguntas sem esperar todo mundo.
- Depois da revelação, até o palpite, todo jogador da rodada pode **segurar "Segure para lembrar"** para ver o papel de novo (some ao soltar). O botão é igual para todos: o impostor também o tem e vê "Você é o impostor!", para que a falta do botão não o denuncie. O servidor nunca envia a palavra ao impostor.

### Discussão
- Dura **3 minutos**. Quando o tempo acaba, a votação começa sozinha.
- O anfitrião pode acrescentar **1 minuto** a cada toque, ou encerrar a discussão antes e começar a votação.
- Durante a discussão, todos veem o histórico completo de perguntas da rodada.

## 6. Regras

### Impostor
- Existe **apenas 1 impostor por rodada**.
- O sorteio favorece quem foi impostor menos vezes na partida, mas **todos os jogadores da rodada sempre podem ser sorteados**. Como os resultados são públicos, excluir quem já foi impostor deixaria os jogadores deduzirem o impostor no fim de cada ciclo.

### Votação
- Votam os jogadores da rodada, em outro jogador da rodada. O voto é **confirmado com um botão** e pode ser **trocado** (confirmando de novo) enquanto a votação estiver aberta; ela fecha assim que todos os conectados votaram.
- Fecha quando **todos os jogadores conectados votarem** ou após **5 minutos**.
- Até o resultado, todos veem só quem já votou, não em quem.
- Quem não votar, ou estiver offline, conta como **abstenção**. Jogador offline não trava a votação.
- O impostor só é **descoberto** se tiver **mais votos do que qualquer outro jogador**.
- **Empate** (de qualquer tipo): o impostor escapa.

### Palpite do impostor
- Se for descoberto, o impostor escolhe a palavra entre ~8 opções da categoria, com a correta entre elas. Sem texto livre.
- No tema "Pessoas da sala", as opções são os nomes dos jogadores da rodada (menos o próprio impostor).
- As opções só são enviadas ao impostor: no tema "Pessoas da sala" elas deixam o impostor de fora, então quem as visse saberia quem ele é antes do resultado.
- Nos temas de palavras, as opções erradas vêm das mesmas palavras ainda não usadas na sala de onde saiu o segredo: um segredo de uma rodada anterior, já revelado, nunca aparece como opção.
- Ele tem **60 segundos**. Sem escolha, conta como palpite errado.

### Resultado
- Abre com a **revelação do impostor**: um destaque passa pelas fotos dos jogadores da rodada, desacelera e para no impostor (toda vez que alguém entra no resultado; dá para pular, e com "reduzir movimento" o impostor aparece direto). Só depois aparecem os votos, que entregariam a resposta.
- Mostra quem era o impostor, o segredo, **quem votou em quem**, o palpite (se houve) e os pontos da rodada, com o placar.
- Novos jogadores podem entrar enquanto o resultado está na tela.

### Pontuação
| Situação | Pontos |
|---|---|
| Jogador votou no impostor | +1 para ele |
| Impostor não foi descoberto | +2 para o impostor |
| Impostor descoberto, mas acerta a palavra | +1 para o impostor |

## 7. Temas

- Os temas são um **catálogo fixo** do jogo. O anfitrião **escolhe entre os disponíveis**, não digita.
- A escolha é feita **no início de cada rodada**, sem lista prévia. O mesmo tema pode ser escolhido várias vezes.
- Existe a opção **"Sorteio"**, que escolhe um tema ao acaso.
- A mesma **palavra** não se repete dentro da sala, mesmo que o tema se repita. Um tema de palavras só é oferecido para uma rodada enquanto tiver pelo menos **4 palavras ainda não usadas** na sala (o palpite do impostor sai delas).
- Cada tema tem o seu **banco de palavras** e o seu **banco de perguntas genéricas**, que servem para qualquer palavra do tema. Assim o impostor consegue blefar e quem sabe a palavra consegue responder.
- Conteúdo inicial: 8 a 10 temas, com ~40 palavras cada. Conteúdo próprio, não copiado de outros jogos.

### Tema "Pessoas da sala"
- Os próprios jogadores da sala são o tema.
- Primeiro sorteia-se o impostor, depois o tema entre os outros jogadores. O impostor não pode ser o tema.
- Todos, menos o impostor, veem o nome sorteado, inclusive a própria pessoa sorteada.
- Banco de perguntas próprio, sobre hábitos e personalidade, com a expressão "essa pessoa" para não depender de gênero. Evitar aparência, saúde e temas sensíveis.
- Todos os jogadores (menos o impostor) sempre podem ser sorteados como tema; quem foi sorteado menos vezes tem mais chance. Assim o impostor não consegue deduzir o nome sorteado pelos resultados anteriores.

## 8. Perguntas e turnos

- **Quem joga a rodada:** os jogadores **conectados** no momento em que o tema é escolhido (mínimo de 3). Quem estiver offline fica fora daquela rodada (não pergunta, não responde e não é impostor) e volta na seguinte.
- **Roda:** a ordem dos jogadores é embaralhada. Cada um **pergunta ao próximo** e o último pergunta ao primeiro. Todos perguntam uma vez e respondem uma vez.
- **Tela de todos:** mostra "quem pergunta para quem" e o texto da pergunta.
- A pergunta é **lida em voz alta** por quem pergunta e **respondida em voz alta**.
- **Avançar:** quem acabou de responder toca em **"Prosseguir"**. Ele é também o próximo a perguntar. O **anfitrião** pode avançar ou pular como reserva.
- **Tempo por turno:** padrão de **3 minutos**, pensado para permitir discussão, repetição da pergunta e tempo para pensar.
- **Estender ou prosseguir:** tanto o anfitrião quanto quem responde podem estender o tempo ou prosseguir antes.
- Extensão de +1 minuto a cada toque, aviso 30 segundos antes do fim e avanço automático quando o tempo acabar.
- No último turno, quem responde toca em "Ir para a discussão".

## 9. Histórico de perguntas

Funcionalidade **muito importante**.

- Todos os participantes podem **voltar e avançar** entre a pergunta anterior e a atual.
- O histórico inclui **somente a rodada atual**.
- Não é possível avançar além do **turno atual**.
- Cada participante tem seu **próprio ponto de visualização**.
- No turno mais recente, a tela acompanha o jogo sozinha. Se a pessoa voltou, a tela não é puxada: aparece um aviso de novo turno com botão "Ir para o atual".
- O botão "Prosseguir" só aparece na visão do turno atual. Quando for a vez de alguém responder ou perguntar, o celular vibra e aparece um aviso fixo, mesmo que a pessoa esteja olhando o histórico.
- Durante a discussão e a votação, o histórico mostra todos os turnos da rodada.
- O servidor envia apenas os turnos até o atual, nunca perguntas futuras. O histórico nunca contém papel nem palavra.

## 10. Fim da partida

- A partida termina depois da **última rodada definida** na criação da sala.
- **Encerrar antes:** existe uma opção para encerrar a partida, com **botão de confirmação** para evitar acidentes (restrita ao anfitrião). O pódio mostra a pontuação até ali.
- Não há limite nem estimativa de duração: as pessoas jogam até terminar.
- Se os jogadores conectados caírem **abaixo de 3** durante a partida, não dá para começar outra rodada, e o anfitrião é avisado de que pode **encerrá-la**.
- O pódio mostra os primeiros colocados, empates dividem a posição.
- "Jogar de novo" mantém a sala e os jogadores, o anfitrião escolhe de novo o número de rodadas e a pontuação recomeça do zero.

## 11. Segurança e integridade

- O **servidor é a fonte da verdade**: a palavra só é enviada às conexões de quem não é impostor, e o impostor só é revelado no resultado.
- Limite de taxa por cliente para criar e entrar em salas (e para a foto e a conexão em tempo real), com uma cota pequena de tentativas com código ou token errados, para que ninguém adivinhe códigos de sala. Validação do nome (tamanho e caracteres).
- Sem token válido, as rotas que pedem token respondem igual para sala inexistente e token errado: não revelam se uma sala existe.
- Teto técnico de 20 jogadores aplicado no servidor.
- Tempo limite nas fases (turno e votação) para ninguém travar o jogo.

## 12. Decisões finais e próximos passos

- Abaixo de 3 jogadores durante a partida: o anfitrião pode encerrar a partida.
- Entrada de novos jogadores: somente no lobby e entre rodadas.
- Pontuação e demais sugestões: aceitas.
- Conteúdo (temas, palavras e perguntas): será gerado na etapa de implementação.
- **Próximo passo:** estrutura do banco de dados PostgreSQL (diagrama e tabelas em alto nível). Depois, mensagens WebSocket entre React e Go.
