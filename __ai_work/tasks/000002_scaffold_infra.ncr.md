```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Task 000002: Scaffold Infrastructure</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            line-height: 1.6;
            color: #24292e;
            max-width: 850px;
            margin: 0 auto;
            padding: 2rem 1rem;
            background-color: #f6f8fa;
        }
        .container {
            background: #ffffff;
            border: 1px solid #e1e4e8;
            border-radius: 6px;
            padding: 2.5rem;
            box-shadow: 0 1px 3px rgba(0,0,0,0.04);
        }
        h1 {
            font-size: 1.8rem;
            border-bottom: 2px solid #eaecef;
            padding-bottom: 0.3em;
            margin-top: 0;
            color: #0366d6;
        }
        h2 {
            font-size: 1.3rem;
            border-bottom: 1px solid #eaecef;
            padding-bottom: 0.3em;
            margin-top: 1.8rem;
            color: #24292e;
        }
        h3 {
            font-size: 1.1rem;
            margin-top: 1.2rem;
        }
        ul, ol {
            padding-left: 1.5rem;
        }
        li {
            margin-bottom: 0.4rem;
        }
        code {
            font-family: SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
            font-size: 85%;
            background-color: rgba(27,31,35,0.05);
            padding: 0.2em 0.4em;
            border-radius: 3px;
        }
        pre {
            background-color: #f6f8fa;
            border-radius: 6px;
            padding: 1rem;
            overflow: auto;
            font-family: SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace;
            font-size: 85%;
            line-height: 1.45;
            border: 1px solid #e1e4e8;
        }
        pre code {
            background-color: transparent;
            padding: 0;
        }
        hr {
            height: 0.25em;
            padding: 0;
            margin: 1.5rem 0;
            background-color: #e1e4e8;
            border: 0;
        }
    </style>
</head>
<body>

<div class="container">
    <h1>Task: 000002_scaffold_infra</h1>

    <h2>Objective</h2>
    <p>Scaffold the local development infrastructure inside <code>./workdir</code>. This task involves configuring a PostgreSQL database using Docker Compose exposed on default port 5432, managing database configuration via connection URI (DSN), updating environment variables, and establishing a robust <code>Makefile</code> with self-documenting help commands to manage the local runtime lifecycle.</p>

    <hr>

    <h2>Directory Structure Updates</h2>
    <p>Add and update the following files under <code>workdir/</code>:</p>

<pre><code>workdir/
├── docker-compose.yml       # PostgreSQL database container setup
├── Makefile                 # Self-documenting build & runner commands
├── config.toml              # Updated with [database] configuration block (using URI)
├── .env.example             # Template environment variables for DB URI & Server
├── internal/
│   └── config/
│       └── config.go        # Updated Config struct to map [database] URI settings
└── ...
</code></pre>

    <hr>

    <h2>Technical Specifications</h2>

    <h3>1. Docker Compose Setup (<code>docker-compose.yml</code>)</h3>
    <ul>
        <li>Define a PostgreSQL service named <code>postgres</code>.</li>
        <li>Use image <code>postgres:16-alpine</code> for a lightweight setup.</li>
        <li>Expose port <code>5432:5432</code> to the host network.</li>
        <li>Use environment variables for container credentials:
            <ul>
                <li><code>POSTGRES_USER</code> (default: <code>postgres</code>)</li>
                <li><code>POSTGRES_PASSWORD</code> (default: <code>postgres</code>)</li>
                <li><code>POSTGRES_DB</code> (default: <code>app_db</code>)</li>
            </ul>
        </li>
        <li>Mount a persistent docker volume named <code>postgres_data</code> to <code>/var/lib/postgresql/data</code>.</li>
        <li>Configure a healthcheck using <code>pg_isready -U ${POSTGRES_USER:-postgres}</code>.</li>
    </ul>

    <h3>2. Configuration &amp; Environment Variables (Database URI approach)</h3>
    <ul>
        <li><strong>Update Config Struct (<code>internal/config/config.go</code>):</strong> Update the singleton configuration struct to support a single database connection URI:
<pre><code>type DatabaseConfig struct {
    URI string `toml:"uri" env:"DATABASE_URL"`
}
</code></pre>
        </li>
        <li><strong>Update <code>config.toml</code>:</strong> Add the default local PostgreSQL connection string:
<pre><code>[database]
uri = "postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable"
</code></pre>
        </li>
        <li><strong>Create <code>.env.example</code>:</strong> Document all supported env variables (including <code>DATABASE_URL</code>) so developers can easily create a <code>.env</code> file:
<pre><code>SERVER_PORT=8080
SERVER_ENV=development
LOG_LEVEL=debug
DATABASE_URL=postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable
</code></pre>
        </li>
    </ul>

    <h3>3. Makefile Automation (<code>Makefile</code>)</h3>
    <ul>
        <li>Create a standard <code>Makefile</code> in <code>workdir/</code>.</li>
        <li>Include a default <code>help</code> target that parses comment annotations (<code>##</code>) and displays an auto-formatted list of available commands.</li>
        <li>Implement the following targets:
            <ul>
                <li><code>make help</code> - Show commands and descriptions.</li>
                <li><code>make dev</code> - Spin up database via docker compose, then run the Go app locally.</li>
                <li><code>make db-up</code> - Start PostgreSQL in background (<code>docker compose up -d postgres</code>).</li>
                <li><code>make db-down</code> - Stop and remove database containers (<code>docker compose down</code>).</li>
                <li><code>make db-logs</code> - Tail PostgreSQL logs.</li>
                <li><code>make build</code> - Compile the API binary to <code>./bin/api</code>.</li>
                <li><code>make run</code> - Execute the compiled binary or run <code>go run cmd/api/main.go</code>.</li>
                <li><code>make test</code> - Execute unit/integration tests (<code>go test -v ./...</code>).</li>
                <li><code>make swagger</code> - Regenerate Swagger documentation (<code>swag init -g cmd/api/main.go -o ./docs</code>).</li>
            </ul>
        </li>
    </ul>

    <hr>

    <h2>Architectural Suggestions</h2>
    <ul>
        <li><strong>Database Driver Pre-wiring:</strong> Ensure <code>github.com/lib/pq</code> or <code>github.com/jackc/pgx/v5</code> is added to <code>go.mod</code>. Using connection strings works seamlessly with <code>sql.Open("postgres", config.Database.URI)</code> or <code>pgxpool.New(ctx, config.Database.URI)</code>.</li>
        <li><strong>Wait-For-IT in Dev Command:</strong> Ensure <code>make dev</code> waits for PostgreSQL container health check to pass before launching <code>go run</code> to avoid initial connection drop issues on clean boots.</li>
    </ul>

    <hr>

    <h2>Validation Steps</h2>
    <ol>
        <li>Run <code>make help</code> and ensure all target commands are listed with descriptions.</li>
        <li>Run <code>make db-up</code> and verify PostgreSQL container starts up successfully on port <code>5432</code>.</li>
        <li>Run <code>make run</code> or <code>make dev</code> and ensure the Go application starts without errors, picking up the <code>DATABASE_URL</code> URI via environment/TOML.</li>
        <li>Verify database container status with <code>docker compose ps</code>.</li>
        <li>Run <code>make db-down</code> to ensure clean teardown.</li>
    </ol>
</div>

</body>
</html>
```