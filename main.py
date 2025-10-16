from fastapi import FastAPI, Header, HTTPException
import subprocess
import os
from dotenv import load_dotenv

# Load environment variables from .env file
load_dotenv()

app = FastAPI()

API_TOKEN = os.getenv("API_TOKEN", "super-secret-token")
PORT = int(os.getenv("PORT", "8000"))

def run_command(cmd: str):
    try:
        result = subprocess.run(
            cmd, shell=True, check=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
        )
        return result.stdout
    except subprocess.CalledProcessError as e:
        return f"Error:\n{e.stderr}"

@app.post("/admin/docker/restart")
def restart_docker_compose(x_admin_token: str = Header(None)):
    if x_admin_token != API_TOKEN:
        raise HTTPException(status_code=401, detail="Unauthorized")

    output = run_command("docker compose restart")
    return {"status": "ok", "output": output}


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
    
    output = run_command(f"""cd {app_path}
          
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
          """)
          
    return {"status": "ok", "output": output}

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=PORT)
