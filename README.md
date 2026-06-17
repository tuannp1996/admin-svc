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
