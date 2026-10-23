---
name: docker-help
description: Explain Docker in plain words and fix common Docker problems.
---

# Skill: Docker help

Use this when the user has a Docker problem or wants their app to run in Docker. The user is a beginner: explain every Docker term in one short sentence.

## Note about Prism's own container
- Prism often runs inside a Docker container itself, so there may be no `docker` command inside it.
- If `docker version` says "command not found", do not install Docker. Tell the user the exact commands to run on their own machine (or their WSL/Ubuntu window) and read the output they paste back.

## Step 1: Write a Dockerfile for their app (only when asked)
- Look at how the app runs: `package.json` scripts, `requirements.txt`, `go.mod`, `Makefile`, README.
- Write a minimal Dockerfile: small official base image (`node:22-alpine`, `python:3.12-slim`, `golang:1.23`), copy only what is needed, install dependencies, `EXPOSE` the port the app listens on, one `CMD` that starts it.
- Add a `.dockerignore` with `node_modules`, `.git`, `.env`.
- For several services, write a `docker-compose.yml`: one service per app, `ports: "8080:8080"` (host port first), `volumes` only for data that must survive restarts.

## Step 2: Read-only diagnosis first (always)
Run these; they change nothing:

```bash
docker ps -a                        # all containers, even stopped ones, with their status
docker compose ps                   # services from docker-compose.yml and whether they are up
docker logs <name> --tail 50        # what the app printed; last 50 lines
docker compose logs --tail 50       # same for every compose service
docker images                       # downloaded images and their sizes
docker system df                    # how much disk Docker uses
```

- Translate the output into plain words: "your container exited 2 minutes ago with code 1, which means the app crashed on start".
- If a command is missing or fails, say so and move on. Do not install tools during diagnosis.

## Step 3: Match the error to a fix
- **"port is already allocated" / "address already in use"** → another program uses that port. Find it: `sudo ss -ltnp | grep :8080` (Linux) or `netstat -ano | findstr 8080` (Windows). Fix: stop the other program, or change the mapping to `"8081:8080"` (host port first).
- **"permission denied" on /var/run/docker.sock** → the user is not in the `docker` group. Fix on the host: `sudo usermod -aG docker $USER`, then log out and back in. Do not suggest running Docker as root.
- **"no space left on device"** → Docker's disk is full. Show what uses space: `docker system df`. Free space only via the safe cleanup in step 4, after asking.
- **"toomanyrequests" / rate limit when pulling an image** → Docker Hub limits anonymous pulls. Fix: `docker login` with a free Docker account, wait and retry, or use a mirror image.
- **WSL: "docker-credential-desktop.exe: exec format error"** → Windows/WSL interop is broken. Follow the fix in the Prism repo's `docs/WINDOWS.md` (Troubleshooting section; it may not exist in the user's project): re-enable WSLInterop binfmt, then `wsl --shutdown` from PowerShell.
- **Build uses old code / cache seems wrong** → the build cache reused an old layer. Fix: `docker compose build --no-cache` (slower but clean). Keep `COPY` order dependencies-first, source-last, so edits do not bust the whole cache.

## Step 4: Safe cleanup (ASK first, always)
- NEVER run `docker system prune`, `docker volume rm`, `docker rm`, or `docker rmi` without asking the user first.
- Before any cleanup, list exactly what would be removed and show it:
  - `docker system df -v` → what uses space.
  - `docker ps -a --filter "status=exited"` → stopped containers.
  - `docker images --filter "dangling=true"` → unnamed leftover images.
  - `docker volume ls` → named data volumes (warn: removing these deletes data).
- Then propose the smallest safe step and wait for a clear yes, e.g. `docker container prune` (stopped containers only) or `docker image prune` (dangling images only).
- Never suggest `docker system prune -a --volumes` to a beginner: it deletes all unused images and volumes, including databases.

## Step 5: Verify and close
- After a fix, rerun the matching read-only command from step 2: `docker compose ps` should show "Up", `docker logs <name>` should show the app started.
- Close with one sentence: what was wrong, what fixed it.
