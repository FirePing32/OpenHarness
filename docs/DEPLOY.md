# Deploying OpenHarness publicly (Coolify)

OpenHarness deploys as **one container**. The builder UI is compiled into the Go binary, and the same process serves the UI, the API, the test previews and the binary builds. The image keeps the Go toolchain because every download is a fresh cross-compile.

```
Internet ─▶ Coolify proxy (Traefik, HTTPS) ─▶ openharness container :7070
                                               ├─ /               builder UI
                                               ├─ /api/*          catalog, validate, preview, build
                                               ├─ /preview/{id}/  sandboxed test agents
                                               └─ /data           volume: Go build cache
```

## Server size

- **Minimum:** 2 vCPU / 4 GB.
  - `docker-compose.yml` is tuned for this: the builder gets 1.5 CPUs and 2.5 GB, leaving room for Coolify's own services.
  - Builds run one at a time (`OPENHARNESS_BUILD_CONCURRENCY=1`). A warm build takes 1–3s.
  - Add **2–4 GB of swap**. The one-off image build compiles six targets and can spike past free RAM on a 4 GB host:
    ```bash
    sudo fallocate -l 4G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
    echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
    ```
- **Comfortable:** 4 vCPU / 8 GB.
  - Raise `cpus` to `3`, `memory` to `6g` and `OPENHARNESS_BUILD_CONCURRENCY` to `2`.
  - **The CPU limit must never exceed the host's CPU count**, or Docker refuses to start the container.
- **Disk:** about 1 GB for the build cache (≈600 MB once all six targets are warm), plus the image (~1.5 GB, since it includes the Go toolchain).

## Coolify setup

1. Install Coolify on the VPS:
   ```bash
   curl -fsSL https://cdn.coollabs.io/coolify/install.sh | sudo bash
   ```
2. Add a DNS **A record** for your domain (e.g. `harness.example.com`) pointing at the VPS.
3. In Coolify, create a **new resource from your Git repository**. Then set:
   - **Build pack:** `Dockerfile` or `Docker Compose` (`docker-compose.yml`). Not Nixpacks: the toolchain must stay in the image. With Docker Compose, add the domain to the `openharness` service with port `7070`. The compose file only `expose`s the port, so the app is reachable solely through Coolify's HTTPS proxy.
   - **Port:** `7070`
   - **Domain:** `https://harness.example.com`
   - **Persistent storage:** a volume mounted at **`/data`**
   - **Health check path:** `/healthz`
   - **Resource limits:** with the Docker Compose build pack they come from `docker-compose.yml` (see Server size). With the Dockerfile build pack, set them in the Resource Limits tab
4. **Deploy.** With the GitHub App connected, every push to the main branch redeploys.

The image already sets everything needed, so no environment variables are required. You can override these:

| Variable | Default in image | Meaning |
|---|---|---|
| `OPENHARNESS_PUBLIC` | `1` | Public mode (see below). Set `0` only for a private, trusted deployment. |
| `OPENHARNESS_TRUST_PROXY` | `1` | Read the client IP from `X-Forwarded-For`. Correct behind Coolify's proxy. Set `0` if the container port is exposed directly. |
| `OPENHARNESS_ADDR` | `0.0.0.0:7070` | Listen address (`PORT` also works). |
| `OPENHARNESS_BUILD_CONCURRENCY` | `2` (`1` in compose) | Parallel binary builds. Each is CPU-bound and uses a few hundred MB of RAM. |

**Do not put model API keys in Coolify's environment.** Public mode never reads server environment variables for visitors' agents anyway.

## What public mode does

Previews run a stranger's spec on your server, so public mode applies these rules:

| Risk | Mitigation |
|---|---|
| A spec reads server secrets (`apiKeyEnv`, `${VAR}` in tool/MCP headers) and sends them to a URL the visitor chose | Server env lookups always return empty. Visitors paste their own key in the Test step; it is held in memory for that instance only. |
| Running commands on the server | The `shell` tool and stdio MCP servers are removed from previews. The visitor sees a note saying so, and the downloaded binary keeps them. |
| SSRF into Coolify's internal network or cloud metadata (`169.254.169.254`) | Every outbound connection is checked **after DNS resolution**. Loopback, private, link-local, CGNAT and multicast addresses are refused. |
| Writing files anywhere | Workspace, memory, sessions and traces are redirected to a per-preview temp dir, deleted when the preview ends. |
| Abuse and cost | Per-IP token buckets: 20 builds/h, 30 test instances/h, 120 messages/h. At most 2 test instances per visitor and 40 overall. Idle instances close after 30 min. |
| Clickjacking / MIME sniffing | `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` |

**Not covered:**
- Previews still run *in-process*. `read_file`/`list_dir`/`write_file` are confined to the preview's temp directory, but a bug in that path check would expose the container.
- For stronger isolation, run each preview in its own microVM (e.g. Fly Machines or Firecracker) and keep the builder as the router.
- Container-level limits (Coolify resource limits, a non-root user, which the image already uses) are your second line of defense.

## Running the image elsewhere

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up --build   # http://localhost:7070
```

The base `docker-compose.yml` publishes no host port, which is right behind a proxy. `docker-compose.local.yml` adds `127.0.0.1:7070` for local use and stops trusting `X-Forwarded-For`.

Any Docker host works the same way: Render, Railway, Fly.io or a plain VPS. Mount a volume at `/data` and route HTTPS to port 7070. Serverless platforms (Vercel, Cloudflare Workers) don't fit: there is no Go toolchain at runtime, executions are time-limited, and previews need a long-lived process.
