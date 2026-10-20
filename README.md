# gogoform

## Rodando as tarefas de IA (`run-tasks`)

O `run-tasks` pega cada tarefa pendente em `__ai_work/tasks/` e executa:
**plano → implementação → code review → commits**, direto no branch atual.

### Pré-requisitos

- `python3`
- CLI do agente instalado e logado: `claude` (padrão) ou `opencode`
- Rodar em um terminal interativo (o runner pode fazer perguntas)

### Passo a passo

```bash
# 1. criar uma tarefa
make new-task NAME=minha_tarefa
# 2. descrever o que fazer no arquivo criado: __ai_work/tasks/NNNNNN_minha_tarefa.md
# 3. rodar
make run-tasks
```

- Tarefa concluída vai para `__ai_work/finished/`; se falhar, vai para `__ai_work/errors/`.
- Rodar `make run-tasks` de novo **retoma** de onde parou.
- Se houver alterações não commitadas, o runner pergunta se você quer fazer stash, commitar,
  descartar ou abortar.

### Opções úteis

```bash
make run-tasks AGENT_CLI_STDOUT=1       # mostra a saída do agente ao vivo
make run-tasks RUNNER_SKIP_REVIEW=1     # pula o code review
make run-tasks RUNNER_RESUME=0          # ignora o progresso salvo e começa do zero
make run-tasks AGENT_CLI=opencode       # usa o opencode em vez do claude
```

Lista completa de opções: [`__ai_work/README.md`](__ai_work/README.md).
