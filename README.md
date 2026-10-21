# gogoform

Construtor de formulários multi-tenant em Go: API de formulários e submissões, UI web em `/app`,
páginas públicas de formulário, uploads, webhooks, cobrança Stripe e e-mail transacional. O código
fica em [`workdir/`](workdir/); o `Makefile` da raiz repassa os alvos de `workdir/Makefile`, então
todos os comandos `make` abaixo funcionam de qualquer um dos dois diretórios.

## Desenvolvimento local

Pré-requisitos: Go 1.22+, Docker (com `docker compose`) e `curl`. As ferramentas de banco
(`dbmate`, `sqlc`, `dbml2sql`) são instaladas com `make setup-tools`.

```bash
cp workdir/.env.example workdir/.env   # ajuste se precisar
make db-up && make migrate-up   # sobe o Postgres e aplica as migrations (repita após novas migrations)
make dev                        # sobe o Postgres, compila o CSS e roda o app em :8080
```

- App: <http://localhost:8080/app> (login com `AUTH_ADMIN_USERNAME` / `AUTH_ADMIN_PASSWORD`).
- Swagger: <http://localhost:8080/swagger/index.html>. Health: <http://localhost:8080/healthz>.
- Adminer (UI do banco): `make adminer` → <http://localhost:8088>.

CSS: o Tailwind é compilado (não há CDN). `make css` baixa a CLI standalone para `workdir/bin/` e
gera `cmd/api/handlers/templates/static/app.css`, que é embutido no binário; `make dev` e
`make build` já rodam `make css`. Durante a edição de templates use `make css-watch` e reinicie o
servidor para embutir o novo CSS.

Testes e checagens:

```bash
make check                                    # go build + go vet
make test                                     # todos os testes (não precisam de banco)
make test PKGS=./internal/pkg/forms/... RUN=TestCreate V=1
make swagger                                  # após mudar anotações dos handlers
make sqlc                                     # após editar db/queries
```

## Configuração

A configuração base é [`workdir/config.toml`](workdir/config.toml); variáveis de ambiente (e o
arquivo `workdir/.env`, carregado na inicialização) sobrescrevem seus valores. Todas as variáveis
estão documentadas em [`workdir/.env.example`](workdir/.env.example), incluindo a seção de produção.
As mais importantes:

| Variável | Para quê |
|---|---|
| `SERVER_ENV`, `SERVER_PORT`, `LOG_LEVEL` | ambiente (`development`/`production`), porta, nível de log |
| `DATABASE_URL` | conexão com o Postgres |
| `APP_BASE_URL` | origem pública: links em e-mails/Stripe e host do Swagger |
| `SUPPORT_EMAIL` | e-mail de contato no rodapé das páginas (opcional; sem ele o link some) |
| `AUTH_JWT_SECRET` | obrigatório fora de `development` (`openssl rand -hex 32`) |
| `AUTH_ADMIN_USERNAME`, `AUTH_ADMIN_PASSWORD` | conta ADMIN criada na inicialização |
| `SECURITY_TRUSTED_PROXIES` | proxies cujo `X-Forwarded-For` é confiável |
| `STRIPE_*`, `SMTP_*`, `MAIL_FROM`, `TURNSTILE_*` | cobrança, e-mail e anti-spam (opcionais) |

Rate limits e checagens de spam ficam em `[security]` no `config.toml`.

## Deploy (VPS com Docker)

Arquitetura ([`workdir/docker-compose.prod.yml`](workdir/docker-compose.prod.yml)):

- **caddy** — única porta pública (80/443): HTTPS automático (Let's Encrypt), HSTS, gzip/zstd e
  proxy reverso para o app ([`workdir/deploy/Caddyfile`](workdir/deploy/Caddyfile)).
- **app** — imagem do GHCR publicada pelo CI (`APP_IMAGE`), com `HEALTHCHECK` em `/healthz`.
- **migrate** — serviço one-shot que roda `dbmate up` antes de o app subir em todo `up`.
- **postgres** — sem porta pública, dados no volume `postgres_data`.
- **backup** — `pg_dump` diário com retenção e envio opcional para S3 (ver abaixo).

O CI ([`.github/workflows/deploy.yml`](.github/workflows/deploy.yml)) roda `go vet`, testes e
`govulncheck`, publica a imagem em `ghcr.io/<owner>/<repo>` e, em pushes para `main` e tags `v*`,
chama `DEPLOY_WEBHOOK_URL`.

### 1. Preparar o servidor

```bash
# Docker Engine + compose plugin, e as ferramentas do deploy.sh
sudo apt install -y jq openssl util-linux webhook   # webhook >= 2.8
sudo useradd -m -G docker deploy
sudo install -d -o deploy /opt/gogoform
sudo -iu deploy git clone https://github.com/<owner>/<repo>.git /opt/gogoform
```

Firewall: libere apenas 22, 80 e 443 (TCP) e 443/UDP (HTTP/3).

DNS: crie registros A/AAAA de `APP_DOMAIN` apontando para o servidor **antes** do primeiro `up`,
para o Caddy conseguir emitir o certificado.

GHCR: se o pacote for privado, faça login como o usuário `deploy` com um token que tenha
`read:packages`:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <usuario-github> --password-stdin
```

### 2. `.env.prod`

```bash
cd /opt/gogoform/workdir
cp .env.example .env.prod && chmod 600 .env.prod
```

Preencha a seção "Production" (`APP_IMAGE`, `APP_DOMAIN`, `ACME_EMAIL`, backups/S3) e revise as
demais: `LOG_LEVEL=info`, `AUTH_JWT_SECRET`, `AUTH_ADMIN_PASSWORD`, `POSTGRES_PASSWORD` (caracteres
seguros para URL, ex. `openssl rand -hex 24`), Stripe, SMTP e Turnstile. Remova `DATABASE_URL`,
`SECURITY_TRUSTED_PROXIES` e `APP_BASE_URL` (o compose define os dois primeiros, e o último vira
`https://$APP_DOMAIN`).

### 3. Primeiro deploy

```bash
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d
docker compose --env-file .env.prod -f docker-compose.prod.yml ps        # app "healthy"
curl https://$APP_DOMAIN/healthz                                          # {"status":"UP",...}
```

`/healthz` faz ping no banco: responde `503` com `"status":"DOWN"` quando ele não responde, e o
`HEALTHCHECK` da imagem (`/app/api -healthcheck`) marca o container como `unhealthy`. As migrations
rodam sozinhas a cada `up` (serviço `migrate`); para ver seus logs:
`docker compose ... logs migrate`.

### 4. Deploy automático (webhook)

`DEPLOY_WEBHOOK_URL` recebe do CI um `POST` com JSON
`{image, repository, digest, tag, sha, ref}` assinado em `X-Hub-Signature-256`
(HMAC-SHA256 do corpo com `DEPLOY_WEBHOOK_SECRET`). No servidor ele precisa: validar a assinatura,
fixar `APP_IMAGE` no digest recebido, rodar `docker compose pull` + `up -d` e responder com erro se
o app não ficar saudável. É isso que [`workdir/scripts/deploy.sh`](workdir/scripts/deploy.sh) faz
(com rollback para a imagem anterior em caso de falha), atrás do receptor
[adnanh/webhook](https://github.com/adnanh/webhook) configurado em
[`workdir/deploy/webhook/`](workdir/deploy/webhook/):

```bash
sudo mkdir -p /etc/gogoform
sudo tee /etc/gogoform/deploy.env >/dev/null <<'EOF'
APP_DIR=/opt/gogoform/workdir
DEPLOY_WEBHOOK_SECRET=<mesmo valor do secret do GitHub>
WEBHOOK_IP=172.30.0.1
EOF
sudo chown deploy /etc/gogoform/deploy.env && sudo chmod 600 /etc/gogoform/deploy.env
sudo cp /opt/gogoform/workdir/deploy/webhook/gogoform-deploy.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now gogoform-deploy
```

O receptor escuta no gateway da rede do compose (`172.30.0.1:9000`, primeiro IP de
`DOCKER_SUBNET`), acessível só pelo Caddy, que encaminha `https://$APP_DOMAIN/hooks/*` para ele.
No GitHub, no environment `production`, configure os secrets:

- `DEPLOY_WEBHOOK_URL` = `https://<APP_DOMAIN>/hooks/deploy`
- `DEPLOY_WEBHOOK_SECRET` = o mesmo valor de `/etc/gogoform/deploy.env`

A resposta só volta quando o deploy termina, então o job do CI falha se o deploy falhar
(`journalctl -u gogoform-deploy` mostra os detalhes). Deploy manual de uma imagem específica:

```bash
scripts/deploy.sh ghcr.io/<owner>/<repo>@sha256:<digest>
```

## Backup e restore

O serviço `backup` faz `pg_dump -Fc` todo dia em `BACKUP_TIME` (UTC) para `BACKUP_DIR`
(padrão `workdir/backups/`, arquivos legíveis só por root), apaga dumps locais com mais de
`BACKUP_RETENTION_DAYS` dias e, com `S3_BUCKET` definido, envia cada dump para o storage
S3-compatível (AWS S3, R2, B2, MinIO...; configure uma regra de lifecycle no bucket para expirar os
remotos). Detalhes em [`workdir/scripts/backup.sh`](workdir/scripts/backup.sh).

```bash
cd /opt/gogoform/workdir
C="docker compose --env-file .env.prod -f docker-compose.prod.yml"
$C run --rm backup run      # backup agora
$C run --rm backup list     # dumps locais e no S3
$C logs backup              # histórico dos backups diários
```

Restore ([`workdir/scripts/restore.sh`](workdir/scripts/restore.sh)): busca o dump (local ou S3),
para o app, roda `pg_restore --clean --if-exists`, aplica as migrations mais novas que o dump e sobe
o app de novo. Pede confirmação (digite `restore`; `-y` pula).

```bash
scripts/restore.sh app_db-20261020T030000Z.dump          # em BACKUP_DIR (ou no S3)
scripts/restore.sh s3://bucket/prefixo/app_db-....dump   # direto do S3
scripts/restore.sh /caminho/para/arquivo.dump            # outro arquivo local
```

Teste o restore periodicamente (ex.: num servidor de staging com uma cópia do `.env.prod`).

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
