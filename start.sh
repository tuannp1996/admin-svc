#!/usr/bin/env bash
# Minimal service control script for admin-svc
# Supports: start | stop | restart | status

set -euo pipefail

# Configurable variables
APP_NAME="admin-svc"
PYTHON_CMD="${PYTHON_CMD:-python3}"
APP_ENTRY="main.py"
WORKDIR="${WORKDIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
LOG_DIR="${LOG_DIR:-${WORKDIR}}"
LOG_FILE="$LOG_DIR/server.log"
PID_FILE="$WORKDIR/$APP_NAME.pid"
NICE_LEVEL="${NICE_LEVEL:-0}"

cd "$WORKDIR"

usage() {
	cat <<EOF
Usage: $0 {start|stop|restart|status}

Environment variables:
	PYTHON_CMD   Path to python executable (default: python3)
	WORKDIR      Working directory for the app (default: script dir)
	LOG_DIR      Directory for server.log (default: WORKDIR)
	NICE_LEVEL   nice level to start the process with (default: 0)

Examples:
	$0 start
	PYTHON_CMD=python3.11 $0 restart
EOF
}

is_running() {
	if [ -f "$PID_FILE" ]; then
		pid=$(cat "$PID_FILE")
		if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
			return 0
		else
			return 1
		fi
	fi
	return 1
}

do_start() {
	if is_running; then
		echo "$APP_NAME already running (pid $(cat "$PID_FILE"))"
		return 0
	fi

	mkdir -p "$(dirname "$LOG_FILE")"

	echo "Starting $APP_NAME..."
	# Start the app in background, capture PID
	nohup nice -n "$NICE_LEVEL" "$PYTHON_CMD" "$APP_ENTRY" > "$LOG_FILE" 2>&1 &
	pid=$!
	# Give it a moment to start
	sleep 0.2
	if kill -0 "$pid" 2>/dev/null; then
		echo "$pid" > "$PID_FILE"
		echo "Started $APP_NAME (pid $pid), logging to $LOG_FILE"
		return 0
	else
		echo "Failed to start $APP_NAME. Check $LOG_FILE for details." >&2
		return 1
	fi
}

do_stop() {
	if ! is_running; then
		echo "$APP_NAME is not running"
		[ -f "$PID_FILE" ] && rm -f "$PID_FILE"
		return 0
	fi

	pid=$(cat "$PID_FILE")
	echo "Stopping $APP_NAME (pid $pid)..."
	kill "$pid" 2>/dev/null || true

	# Wait up to 10s for graceful shutdown
	for i in {1..50}; do
		if kill -0 "$pid" 2>/dev/null; then
			sleep 0.2
		else
			break
		fi
	done

	if kill -0 "$pid" 2>/dev/null; then
		echo "Process did not exit, sending SIGKILL..."
		kill -9 "$pid" 2>/dev/null || true
	fi

	rm -f "$PID_FILE"
	echo "$APP_NAME stopped"
}

do_status() {
	if is_running; then
		echo "$APP_NAME is running (pid $(cat "$PID_FILE"))"
		return 0
	else
		echo "$APP_NAME is not running"
		return 1
	fi
}

case "${1:-}" in
	start)
		do_start
		;;
	stop)
		do_stop
		;;
	restart)
		do_stop
		do_start
		;;
	status)
		do_status
		;;
	-h|--help|help|"")
		usage
		;;
	*)
		echo "Unknown command: $1" >&2
		usage
		exit 2
		;;
esac

exit 0