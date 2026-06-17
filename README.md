# admin-svc

Lightweight Go service that monitors your server and sends Telegram alerts.

## What it monitors

| Check type | What it does |
|---|---|
| 🐳 **Docker** | Checks if named containers are running |
| 🏥 **HTTP Health** | GET request → validates status code |
| 🔁 **API/Curl** | Any method, custom headers & body → validates status |
| 🌐 **Page** | GET page → validates status + optional text presence |

Alerts are **deduplicated**: you get one alert when something breaks, and one recovery message when it comes back. No spam.

## Quick start

### 1. Configure

Edit `config.yaml`:

```yaml
telegram:
  bot_token: "${TELEGRAM_BOT_TOKEN}"   # or paste directly
  chat_id: "${TELEGRAM_CHAT_ID}"

scheduler:
  interval_seconds: 60

docker:
  enabled: true
  containers:
    - name: "nginx"
      alert_on_stopped: true

health_checks:
  enabled: true
  endpoints:
    - name: "My API"
      url: "http://localhost:8080/health"
      expected_status: 200
```

### 2. Run with Docker Compose

```bash
export TELEGRAM_BOT_TOKEN=xxx
export TELEGRAM_CHAT_ID=yyy
docker compose up -d
```

### 3. Run as binary

```bash
go build -o admin-svc ./cmd/main.go
./admin-svc -config config.yaml
```

## Environment variable support

All values in `config.yaml` support `${ENV_VAR}` substitution, so you can keep secrets out of the file.

## GitHub Actions CI/CD (Deploy to VPS)

This repo includes a workflow at `.github/workflows/cicd.yml`.

- On every pull request: runs `go test ./...` and validates Docker build.
- On push to `main`: runs CI, then deploys to VPS over SSH.

### Required GitHub Secrets

Add these in **GitHub → Settings → Secrets and variables → Actions**:

- `VPS_HOST`: VPS IP or domain.
- `VPS_USER`: SSH user used for deployment.
- `VPS_SSH_KEY`: Private SSH key (PEM/OpenSSH format) for `VPS_USER`.
- `VPS_PORT`: SSH port (usually `22`).
- `VPS_APP_DIR`: Absolute path on VPS where this repository exists.

### VPS prerequisites

On your VPS, make sure:

1. Docker and Docker Compose are installed.
2. SSH access from VPS to this GitHub repository is configured (for `git clone/pull`).
3. `config.yaml` and required environment variables are set for runtime.
4. The SSH user has permission to run Docker commands.

Deployment command executed by the workflow:

```bash
if [ -d "$VPS_APP_DIR/.git" ]; then
  cd "$VPS_APP_DIR"
  git pull --ff-only origin main
else
  git clone --branch main "git@github.com:<owner>/<repo>.git" "$VPS_APP_DIR"
  cd "$VPS_APP_DIR"
fi
docker compose up -d --build
```

## How to get a Telegram bot

1. Message [@BotFather](https://t.me/BotFather) → `/newbot`
2. Copy the bot token
3. Add bot to your group or get your chat ID via `https://api.telegram.org/bot<TOKEN>/getUpdates`

## Project structure

```
admin-svc/
├── cmd/main.go                  # Entry point, wiring
├── internal/
│   ├── config/config.go         # YAML config loader
│   ├── docker/checker.go        # Docker container checks
│   ├── health/checker.go        # HTTP / curl / page checks
│   ├── telegram/notifier.go     # Telegram message sender
│   └── scheduler/cron.go        # Orchestrator + alert deduplication
├── config.yaml
├── Dockerfile
└── docker-compose.yml
```
