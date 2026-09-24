Atue como um Engenheiro de Software Sênior especializado em Go, PostgreSQL, DevOps e automação de banco de dados.

### OBJETIVO
Criar a automação completa de migrations e geração de código Go para o projeto "Guizzly", integrando DBML, dbml2sql, dbmate e sqlc através de um script de setup automatizado e um Makefile orquestrador.

---

### ESTRUTURA E ARQUIVOS DE ENTRADA/SAÍDA

1. **Entrada do DBML**: O arquivo de schema DBML estará localizado estritamente em `__ai_work/inputs/db_schema.dbml`.
2. **Saída do SQL gerado**: O SQL convertido do DBML deve ser gerado em `__ai_work/outputs/schema.sql`.
3. **Diretório de Migrations**: As migrations do dbmate devem ser salvas na pasta `db/migrations/`.
4. **Queries do SQLC**: As consultas SQL consumidas pelo sqlc devem ficar em `db/queries/`.
5. **Código Go Gerado**: O código gerado pelo sqlc deve ir para `internal/db/`.

---

### REQUISITOS DO SCRIPT DE SETUP (`scripts/setup_migrations.sh`)

Crie um script Bash executável e resiliente (`scripts/setup_migrations.sh`) que execute o seguinte fluxo:

1. **Checagem e Instalação de Dependências**:
   - Verificar se o `dbml2sql` (pacote `@dbml/cli` via Node.js/npm) está instalado. Se não estiver, instalá-lo globalmente (`npm install -g @dbml/cli`).
   - Verificar se o `dbmate` está instalado no sistema. Se não estiver, baixá-lo ou exibir instruções/comando de instalação apropriado para o SO (ex: `brew install dbmate` ou via binary release).
   - Verificar se o `sqlc` está instalado no sistema. Se não estiver, exibir instruções ou efetuar a instalação (ex: `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`).

2. **Geração do Schema SQL**:
   - Garantir a existência do diretório `__ai_work/outputs/`.
   - Executar o comando:
     `dbml2sql --postgres __ai_work/inputs/db_schema.dbml -o __ai_work/outputs/schema.sql`

3. **Geração da Migration do dbmate**:
   - Verificar se já existe uma migration inicial no diretório `db/migrations/`.
   - Se não existir, criar uma nova migration via dbmate: `dbmate new initial_schema`.
   - Ler o conteúdo gerado em `__ai_work/outputs/schema.sql` e injetá-lo como o bloco `-- migrate:up` no arquivo `.sql` recém-criado pelo dbmate em `db/migrations/`.
   - Incluir comandos seguros de `DROP TABLE IF EXISTS ... CASCADE` no bloco `-- migrate:down`.

4. **Aplicação das Migrations**:
   - Executar `dbmate up` para aplicar todas as migrations pendentes no banco de dados PostgreSQL configurado na variável de ambiente `DATABASE_URL` (com fallback para `postgres://postgres:postgres@localhost:5432/guizzly?sslmode=disable`).

---

### CONFIGURAÇÃO DO SQLC (`sqlc.yaml`) E QUERIES INICIAIS

1. **Configuração `sqlc.yaml`**:
   - Crie o arquivo de configuração `sqlc.yaml` apontando para o motor `postgresql`.
   - Configure o schema para ler os arquivos `.sql` do diretório `db/migrations/` (ou do `__ai_work/outputs/schema.sql`).
   - Configure o diretório de queries para `db/queries/`.
   - Configure o pacote de saída Go para `package db` na pasta `internal/db/`.
   - Ative suporte a `json`/`yaml` tags e tipos SQL nulos amigáveis do Go.

2. **Queries Iniciais (`db/queries/quizzes.sql`)**:
   - Crie exemplos de consultas SQL que serão compiladas pelo sqlc:
     - `CreateTenant` (INSERT)
     - `CreateQuiz` (INSERT com retorno de ID e dados)
     - `GetQuizByID` (SELECT com JOIN em questões e opções)
     - `ListQuizzesByTenant` (SELECT paginado com filtros)
     - `CreatePool` e `AddQuizToPool` (INSERTs para gerenciamento de pools)

---

### MAKEFILE ORQUESTRADOR (`Makefile`)

Crie um `Makefile` na raiz do projeto com comandos organizados na ordem correta de execução:

- `make setup-tools`: Roda a checagem e instalação do dbml2sql, dbmate e sqlc.
- `make generate-schema`: Executa o `dbml2sql` para converter o DBML de `__ai_work/inputs/db_schema.dbml` para `__ai_work/outputs/schema.sql`.
- `make migrate-new NAME=...`: Cria uma nova migration do dbmate.
- `make migrate-up`: Executa `dbmate up` para aplicar as migrations pendentes no banco.
- `make migrate-down`: Executa `dbmate down` para reverter a última migration.
- `make sqlc`: Executa `sqlc generate` para compilar as queries SQL em código Go fortemente tipado na pasta `internal/db/`.
- `make build-db`: Target mestre que encadeia a execução na ordem correta:
  `generate-schema` -> `migrate-up` -> `sqlc`.

---

### SAÍDA ESPERADA

Entregue os seguintes arquivos completamente preenchidos e operacionais:
1. `scripts/setup_migrations.sh` (com permissão `chmod +x`)
2. `sqlc.yaml`
3. `db/queries/quizzes.sql`
4. `Makefile`