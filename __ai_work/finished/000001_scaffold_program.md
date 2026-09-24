```html

<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Task 000001: Scaffold Program</title>
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
        .badge {
            display: inline-block;
            padding: 0.25em 0.4em;
            font-size: 75%;
            font-weight: 700;
            line-height: 1;
            text-align: center;
            white-space: nowrap;
            vertical-align: baseline;
            border-radius: 0.25rem;
            color: #fff;
            background-color: #28a745;
        }
    </style>
</head>
<body>

<div class="container">
    <h1>Task: 000001_scaffold_program</h1>

    <h2>Objective</h2>
    <p>Scaffold the base Go project inside <code>./workdir</code> with a standard, production-ready directory structure. The application must feature standard library HTTP routing, dynamic logger configuration, TOML-based configuration with environment variable overrides (singleton pattern), auto-generated Swagger documentation, and a clean <code>/healthz</code> health check endpoint.</p>

    <hr>

    <h2>Directory Structure Requirements</h2>
    <p>Organize the code under <code>workdir/</code> following standard Go project layouts:</p>

<pre><code>workdir/
├── cmd/
│   └── api/
│       └── main.go              # Entry point (parses --config flag, initializes logger/config, starts server)
├── internal/
│   ├── config/
│   │   └── config.go            # Config struct, TOML parsing, Env overrides, Singleton logic
│   ├── handler/
│   │   └── health.go            # Health check HTTP handler with Swagger annotations
│   └── logger/
│       └── logger.go            # Structured logging wrapper (using log/slog with level support)
├── docs/                        # Generated Swagger files (docs.go, swagger.json, swagger.yaml)
├── config.toml                  # Default configuration file template
├── go.mod
└── go.sum</code></pre>

    <hr>

    <h2>Technical Specifications</h2>

    <h3>1. Project Initialization &amp; HTTP Server</h3>
    <ul>
        <li>Initialize Go module if not present (e.g., <code>go mod init app</code>).</li>
        <li><strong>REST Server:</strong> Use <strong>ONLY standard library</strong> (<code>net/http</code>, <code>http.ServeMux</code>) for HTTP handling and routing. Do <strong>NOT</strong> use external router frameworks like Chi, Gin, or Fiber.</li>
        <li>Implement a health check handler mapped to <code>GET /healthz</code>.
            <ul>
                <li>Response format: <code>{"status": "UP", "timestamp": "&lt;ISO-8601&gt;"}</code> with status <code>200 OK</code>.</li>
            </ul>
        </li>
    </ul>

    <h3>2. Configuration (<code>internal/config</code>)</h3>
    <ul>
        <li><strong>Format:</strong> Use TOML for file configuration (e.g., <code>github.com/Pelletier/go-toml/v2</code> or <code>github.com/BurntSushi/toml</code>).</li>
        <li><strong>Configuration Struct (<code>Config</code>):</strong>
<pre><code>type Config struct {
    Server struct {
        Port int    `toml:"port" env:"SERVER_PORT"`
        Env  string `toml:"env"  env:"SERVER_ENV"`
    } `toml:"server"`
    Logger struct {
        Level string `toml:"level" env:"LOG_LEVEL"` // "debug", "info", "warn", "error"
    } `toml:"logger"`
}</code></pre>
        </li>
        <li><strong>File / CLI Flag Loading:</strong>
            <ul>
                <li>Support a CLI flag <code>--config</code> (e.g., <code>go run cmd/api/main.go --config=path/to/config.toml</code>).</li>
                <li>Default file location if <code>--config</code> is not provided: <code>./config.toml</code>.</li>
            </ul>
        </li>
        <li><strong>Precedence &amp; Singleton Pattern:</strong>
            <ul>
                <li>Implement a thread-safe Singleton pattern using <code>sync.Once</code> (<code>GetConfig() *Config</code>).</li>
                <li>Loading sequence:
                    <ol>
                        <li>Read and parse TOML file into struct.</li>
                        <li>Read OS Environment variables (<code>SERVER_PORT</code>, <code>SERVER_ENV</code>, <code>LOG_LEVEL</code>).</li>
                        <li><strong>Environment variables MUST take precedence</strong> over TOML values if present.</li>
                    </ol>
                </li>
            </ul>
        </li>
    </ul>

    <h3>3. Structured Logging (<code>internal/logger</code>)</h3>
    <ul>
        <li>Use Go standard library package <code>log/slog</code>.</li>
        <li>Wrap or initialize <code>slog.Logger</code> based on <code>Config.Logger.Level</code>:
            <ul>
                <li>Dynamically set log level (<code>slog.LevelDebug</code>, <code>slog.LevelInfo</code>, <code>slog.LevelWarn</code>, <code>slog.LevelError</code>).</li>
                <li>Log output should be formatted as JSON in production (<code>SERVER_ENV=production</code>) or Text in development (<code>SERVER_ENV=development</code>).</li>
            </ul>
        </li>
    </ul>

    <h3>4. Swagger Auto-Documentation</h3>
    <ul>
        <li>Integrate <code>swag</code> annotations for Go.</li>
        <li>Add general API annotations to <code>cmd/api/main.go</code> (<code>@title</code>, <code>@version</code>, <code>@description</code>, <code>@host</code>).</li>
        <li>Add endpoint annotations to the <code>/healthz</code> handler in <code>internal/handler/health.go</code>:
            <ul>
                <li>Success response 200 description.</li>
            </ul>
        </li>
        <li>Use <code>github.com/swaggo/http-swagger</code> (or standard <code>http.FileServer</code> serving generated specs) to expose the Swagger UI under <code>GET /swagger/</code>.</li>
        <li>Ensure <code>swag init -g cmd/api/main.go -o ./docs</code> or code-level equivalent is configured, and generated files in <code>docs/</code> are imported.</li>
    </ul>

    <hr>

    <h2>Configuration Template (<code>config.toml</code>)</h2>
    <p>Create a baseline <code>config.toml</code> in <code>workdir/</code>:</p>

<pre><code>[server]
port = 8080
env = "development"

[logger]
level = "debug"</code></pre>

    <hr>

    <h2>Validation Steps</h2>
    <p>Before finishing, execute the following to verify implementation correctness:</p>
    <ol>
        <li>Run <code>go mod tidy</code> to clean up dependencies.</li>
        <li>Build and run the server using <code>go run cmd/api/main.go</code>.</li>
        <li>Verify <code>GET http://localhost:8080/healthz</code> returns JSON <code>{"status": "UP", ...}</code>.</li>
        <li>Verify <code>GET http://localhost:8080/swagger/index.html</code> loads the Swagger UI.</li>
        <li>Verify overriding config via env vars works (e.g., <code>LOG_LEVEL=error SERVER_PORT=9090 go run cmd/api/main.go</code>).</li>
        <li>Run <code>go test ./...</code> and ensure no compilation or runtime errors.</li>
    </ol>
</div>

</body>
</html>

```