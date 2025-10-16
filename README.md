# admin-svc

A small Python service for admin tasks.

## What this repository contains

- `main.py` — service entrypoint
- `requirements.txt` — Python dependencies
- `env.example` — example environment variables

## Prerequisites

- Python 3.10+ (or system default from `python3`)
- pip

## Setup

1. Create and activate a virtual environment (recommended):

```bash
python3 -m venv .venv
source .venv/bin/activate
```

2. Install dependencies:

```bash
pip install -r requirements.txt
```

3. Create a `.env` from `env.example` and fill values as needed:

```bash
cp env.example .env
# edit .env
```

## Run

Run the service with:

```bash
python main.py
```

## Notes

- If the project grows, consider adding tests and CI config.
- Send me any additional details you'd like included in this README.