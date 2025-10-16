from fastapi import FastAPI, Header, HTTPException
import subprocess
import os
import uuid
import threading
from datetime import datetime
from dotenv import load_dotenv

# Load environment variables from .env file
load_dotenv()

app = FastAPI()

API_TOKEN = os.getenv("API_TOKEN", "super-secret-token")
PORT = int(os.getenv("PORT", "8000"))

def run_command(cmd: str, name: str = "cmd") -> dict:
    """Run a shell command in the background and write combined stdout/stderr to a log file.

    Returns a dict with `pid` and `log_path`.
    """
    os.makedirs("logs", exist_ok=True)
    timestamp = datetime.utcnow().strftime("%Y%m%dT%H%M%SZ")
    unique = uuid.uuid4().hex[:8]
    safe_name = name.replace("/", "_").replace(" ", "_")
    log_filename = f"{safe_name}_{timestamp}_{unique}.log"
    log_path = os.path.join("logs", log_filename)

    # Open log file and start process in background. Combine stdout and stderr.
    logfile = open(log_path, "a")
    try:
        # On UNIX, preexec_fn=os.setpgrp detaches child into its own process group.
        proc = subprocess.Popen(
            cmd,
            shell=True,
            stdout=logfile,
            stderr=subprocess.STDOUT,
            preexec_fn=os.setpgrp,
        )
        pid = proc.pid
        # Close logfile in parent; child keeps the fd open.
        logfile.close()
        return {"pid": pid, "log_path": log_path}
    except Exception as e:
        # Make sure logfile is closed on error and return error info
        try:
            logfile.write(f"Failed to start command: {e}\n")
            logfile.close()
        except Exception:
            pass
        return {"error": str(e), "log_path": log_path}


def trigger_command(cmd: str, name: str = "cmd") -> dict:
    """Start a subprocess and return immediately with job info.

    The subprocess is started in the parent process to obtain its PID, and a
    background thread waits for completion and writes final status to the log.
    Returns {"job_id": <uuid>, "pid": <pid>, "log_path": <path>}
    """
    os.makedirs("logs", exist_ok=True)
    timestamp = datetime.utcnow().strftime("%Y%m%dT%H%M%SZ")
    job_id = uuid.uuid4().hex
    safe_name = name.replace("/", "_").replace(" ", "_")
    log_filename = f"{safe_name}_{timestamp}_{job_id[:8]}.log"
    log_path = os.path.join("logs", log_filename)

    # Open logfile for the child to write into
    logfile = open(log_path, "a")
    try:
        proc = subprocess.Popen(
            cmd,
            shell=True,
            stdout=logfile,
            stderr=subprocess.STDOUT,
            preexec_fn=os.setpgrp,
        )
    except Exception as e:
        try:
            logfile.write(f"Failed to start command: {e}\n")
            logfile.close()
        except Exception:
            pass
        return {"error": str(e), "log_path": log_path}

    pid = proc.pid

    def waiter(p: subprocess.Popen, path: str, jid: str):
        try:
            ret = p.wait()
            with open(path, "a") as f:
                f.write(f"\n[{datetime.utcnow().isoformat()}] Job {jid} finished with return code {ret}\n")
        except Exception as ex:
            try:
                with open(path, "a") as f:
                    f.write(f"\n[{datetime.utcnow().isoformat()}] Job {jid} waiter error: {ex}\n")
            except Exception:
                pass

    t = threading.Thread(target=waiter, args=(proc, log_path, job_id), daemon=True)
    t.start()

    # Parent can close its handle to logfile; child still has FD open.
    try:
        logfile.close()
    except Exception:
        pass

    return {"job_id": job_id, "pid": pid, "log_path": log_path}

@app.post("/admin/docker/restart")
def restart_docker_compose(x_admin_token: str = Header(None)):
    if x_admin_token != API_TOKEN:
        raise HTTPException(status_code=401, detail="Unauthorized")

    result = trigger_command("docker compose restart", name="docker_compose_restart")
    return {"status": "ok", "result": result}


@app.post("/admin/docker/restart/{service}")
def restart_service(service: str, x_admin_token: str = Header(None)):
    if x_admin_token != API_TOKEN:
        raise HTTPException(status_code=401, detail="Unauthorized")

    # Get environment variables
    gh_token = os.getenv("GH_TOKEN")
    github_actor = os.getenv("GITHUB_ACTOR")
    registry = os.getenv("REGISTRY")
    image_name = os.getenv("IMAGE_NAME")
    app_path = os.getenv("APP_PATH", "/opt/app-name")
    
    if not all([gh_token, github_actor, registry, image_name]):
        return {"status": "error", "output": "Missing required environment variables: GH_TOKEN, GITHUB_ACTOR, REGISTRY, IMAGE_NAME"}
    
    cmd = f"""cd {app_path}
          
        echo "Logging in to GHCR..."
        echo "{gh_token}" | docker login ghcr.io -u {github_actor} --password-stdin

        echo "Pulling latest image from GHCR..."
        docker pull {registry}/{image_name}:latest

        echo "Stopping existing containers..."
        docker compose down --remove-orphans

        echo "Starting services with new image..."
        docker compose up -d

        echo "Cleaning up unused images..."
        docker image prune -f

        echo "Current running containers:"
        docker ps

        echo "Waiting for application to start..."
        sleep 30
        """

    result = trigger_command(cmd, name=f"restart_{service}")
    return {"status": "ok", "result": result, "message": "trigger received!"}

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=PORT)
