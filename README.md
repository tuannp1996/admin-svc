# admin-svc

Lightweight Go service that monitors your server and sends Telegram alerts.

## What it monitors

| Check type | What it does |
|---|---|
| 🐳 **Docker** | Checks if named containers are running |
| 🏥 **HTTP Health** | GET request → validates status code |
| 🔁 **API/Curl** | Any method, custom headers & body → validates status |
| 🌐 **Page** | GET page → validates status + optional text presence + optional async recovery command |

Alerts are **deduplicated**: you get one alert when something breaks, and one recovery message when it comes back. No spam.

## Telegram commands

The bot now supports runtime commands from the configured `chat_id`:

- `/help`: lists all available commands and their usage.
- `/status`: returns current monitor summary and number of active alerts.
- `/restart <container_name>`: restarts a Docker container by name via Docker socket.
- `/blog_gen <topic>` or `/gen_blog <topic>`: triggers external `auto_blog` service via HTTP. Topics must contain at least 4 words.
- `/blog_topic "<topic1>" "<topic2>" ...`: publish one or multiple topics of at least 4 words into the configured Redis stream.
- `/blog_articles [status] [limit]`: list recent articles; status defaults to `pending` and limit defaults to 10.
- `/blog_view <article_id>`: show article status and metadata.
- `/blog_approve <article_id>`: approve a pending article.
- `/blog_publish <article_id>`: publish an approved article.
- `/blog_approve_publish <article_id>`: approve and publish in one action.
- `/blog_hide <article_id>`: hide a published article.
- `/blog_cover <id|slug> <minio_image_path>`: assign an existing MinIO image as an article cover.
- `/tik_users`: fetch TikTok users from the configured API client.
- `/exec <command>`: run an allowlisted system command.


## Quick start

### 1. Configure

Edit `config.yaml`:

```yaml
telegram:
  bot_token: "${TELEGRAM_BOT_TOKEN}"   # or paste directly
  chat_id: "${TELEGRAM_CHAT_ID}"

blog_admin:
  base_url: "http://localhost:8087/api/admin/service"
  timeout_seconds: 30

scheduler:
  interval_seconds: 60
  jobs:
    - name: "blog_gen"
      enabled: true
      cron: "0 */4 * * * *"
      service: "BLOG-AUTO"
      api: "BLOG Gen Article"
      topic_source: "redis"
      redis_addr: "localhost:6379"
      redis_topic_stream: "blog:topics:stream"

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

page_checks:
  enabled: true
  pages:
    - name: "Landing Page"
      url: "https://financi.vn"
      expected_status: 200
      recovery_command: "pm2 start financi-web"
      recovery_timeout_seconds: 120
```

When a page check first transitions from healthy to failed, admin-svc executes recovery_command asynchronously one time for that outage (for example after HTTP 502). It runs again only after the check recovers and fails later.

### Scheduler cron jobs

You can run multiple named cron jobs that trigger configured API clients.

- `name`: job label shown in logs/alerts.
- `cron`: cron expression (supports both 5-field and 6-field with optional seconds).
- `service`: service name from `clients.service[].name` (optional but recommended).
- `api`: API name from `clients.service[].api[].name`.
- `topic`: optional static topic payload for API triggers that accept it.
- `topic_source`: `static` (default) or `redis`.
- `redis_addr`, `redis_password`, `redis_db`, `redis_topic_stream`, `redis_topic_wait_seconds`, `redis_topic_max_retries`, `redis_topic_dead_letter_stream`: redis options when `topic_source: redis`.

If a cron job fails, admin-svc sends a deduplicated `Cron Job` alert to Telegram and sends recovery when it succeeds again.
After a blog article is generated successfully, the bot sends its ID, slug, summary, and ready-to-use commands for setting the cover, approving, or approving and publishing it.
The daily `tik_users` cron sends the successful API response to Telegram at 20:00 Asia/Ho_Chi_Minh time.

Topic behavior:

- `static`: uses `topic` as-is each run.
- `redis`: waits for a topic using blocking `XREAD` from `redis_topic_stream`. The message is deleted (`XDEL`) only after API call succeeds, so failed blog generation is retried automatically on later cron runs. When retries reach `redis_topic_max_retries`, the message is moved to `redis_topic_dead_letter_stream` and removed from the main stream.

Blog topics are validated before publishing, manual generation, and cron generation; each topic must contain at least 4 whitespace-separated words.
Invalid topics already present in Redis are deleted and skipped without calling the article generation API.

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
2. `config.yaml` and required environment variables are set for runtime.
3. The SSH user has permission to run Docker commands.

Deployment command executed by the workflow:

```bash
IMAGE="ghcr.io/<owner>/<repo>:latest"
docker pull "$IMAGE"
export DOCKER_IMAGE="$IMAGE"
docker compose -f "$VPS_APP_DIR/docker-compose.yml" up -d --no-build
```

## How to get a Telegram bot

1. Message [@BotFather](https://t.me/BotFather) → `/newbot`
2. Copy the bot token
3. Add bot to your group or get your chat ID via `https://api.telegram.org/bot<TOKEN>/getUpdates`

## Project structure

```
admin-svc/
├── cmd/main.go                  # Entry point, wiring
├── cmd/telegram_commands.go     # Telegram command handlers composition
├── internal/
│   ├── domain/notification.go   # Core entity
│   ├── config/config.go         # YAML config loader
│   ├── usecase/port/notifier.go # Usecase port
│   ├── usecase/notification/     # Notification usecase
│   ├── docker/checker.go        # Docker container checks
│   ├── health/checker.go        # HTTP / curl / page checks
│   ├── service/statistics.go    # Orchestrator + alert deduplication
│   ├── infrastructure/telegram/ # Telegram adapter implementation
│   ├── scheduler/cron.go        # Compatibility wrapper to service
│   └── telegram/notifier.go     # Compatibility wrapper to infrastructure
├── config.yaml
├── Dockerfile
└── docker-compose.yml
```
