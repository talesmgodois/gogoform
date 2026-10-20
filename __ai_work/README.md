# Runner de tarefas (`run-tasks`)

Pipeline automático que pega os arquivos pendentes em `__ai_work/tasks/` e executa cada um em
quatro fases: **plano → implementação → code review → commits**. Ao final, o arquivo é movido
para `__ai_work/finished/` (ou `__ai_work/errors/` se falhar).

## 1. Criar uma tarefa

```bash
make new-task NAME=meu_nome_da_tarefa
```

Cria `__ai_work/tasks/0000NN_meu_nome_da_tarefa.md`. Abra o arquivo e descreva o que deve ser feito
(quanto mais preciso, melhor).

## 2. Rodar

```bash
make run-tasks
```

- Processa **todas** as tarefas pendentes de `tasks/`, na ordem numérica.
- Se a árvore de trabalho estiver suja (alterações não commitadas), o runner pergunta o que fazer
  (stash / commitar / descartar / abortar) — rode em um terminal interativo.
- Progresso fica salvo em `__ai_work/errors/track/`; rodar de novo **retoma** de onde parou.

### Opções (variáveis de ambiente ou flags do make)

| Variável | Default | O que faz |
|---|---|---|
| `AGENT_CLI` | `claude` | CLI do agente usado (`claude` ou `opencode`) |
| `AGENT_CLI_STDOUT` | `0` | `1` = mostra a saída do agente em tempo real |
| `RUNNER_RESUME` | `1` | `0` = descarta o progresso salvo e começa do zero |
| `RUNNER_SKIP_REVIEW` | `0` | `1` = pula o code review de todas as tarefas (como se fossem `.ncr.md`) |
| `RUNNER_PUSH` | `0` | `1` = faz push do branch ao final |
| `MAX_FIX_ATTEMPTS` | `1` | Rodadas de correção após um code review reprovado |
| `MAX_TURNS` | — | Limite de turnos por sessão do agente |
| `MAX_BUDGET_USD` | — | Orçamento máximo em USD por tarefa |
| `MODEL_PLAN` / `MODEL_IMPLEMENT` / `MODEL_REVIEW` / `MODEL_COMMITS` | — | Modelo por fase |
| `EFFORT_PLAN` / `EFFORT_IMPLEMENT` / `EFFORT_REVIEW` / `EFFORT_COMMITS` | — | Nível de esforço por fase |
| `TODOS_PER_SESSION` | `1` | Quantos todos do plano executar por sessão |

Exemplos:

```bash
# ver a saída do agente ao vivo, com mais uma chance de correção
make run-tasks AGENT_CLI_STDOUT=1 MAX_FIX_ATTEMPTS=2

# rodar sem o code review
make run-tasks RUNNER_SKIP_REVIEW=1

# listar os modelos válidos para MODEL_IMPLEMENT (e as outras MODEL_*)
make list-models
make list-models AGENT_CLI=opencode
make list-models ALL=1

# rodar de zero, com orçamento de 5 USD por tarefa
make run-tasks RUNNER_RESUME=0 MAX_BUDGET_USD=5

# outro agente/modelo
make run-tasks AGENT_CLI=opencode MODEL_IMPLEMENT=sonnet EFFORT_REVIEW=high
```

## 3. Saída

- `__ai_work/tasks/` — tarefas ainda não concluídas (próxima execução continua daqui).
- `__ai_work/finished/` — tarefas concluídas.
- `__ai_work/errors/` — tarefas que falharam + relatórios de review.
- `__ai_work/reviews/` — relatórios de code review gerados.

Ver o resumo do que está aberto: use o comando `/open-tasks` no agente.

## Dicas

- Arquivos terminados em `.ncr.md` pulam o code review.
- Se um erro interromper a execução, corrija o problema e rode `make run-tasks` de novo — ele retoma.
- Comandos e convenções do projeto: ver `AGENTS.md` na raiz.
