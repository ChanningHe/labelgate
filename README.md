
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/icon-light.svg" />
    <source media="(prefers-color-scheme: light)" srcset="assets/icon-dark.svg" />
    <img alt="Labelgate" src="assets/icon-dark.svg" width="48" height="48" />
  </picture>
</p>

<h1 align="center">Labelgate</h1>

<p align="center">
  A simple tool for managing Cloudflare DNS, Tunnels, and Zero Trust Access using Docker labels.
</p>

---

## Documentation

Full documentation is available at [labelgate-docs.pages.dev](https://labelgate-docs.pages.dev/).

## Quick Start

Create a `.env` file (keep it out of version control):

```bash
LABELGATE_CLOUDFLARE_API_TOKEN=your-api-token
LABELGATE_CLOUDFLARE_ACCOUNT_ID=your-account-id
LABELGATE_CLOUDFLARE_TUNNEL_ID=your-tunnel-id
# Required for the API and dashboard; without it both are disabled. At least 16 characters.
# Generate one with: openssl rand -hex 32
LABELGATE_API_TOKEN=a-long-random-string
# Authenticate cloudflared (not for labelgate)
TUNNEL_TOKEN=your-tunnel-token
```

Create `compose.yaml`:

```yaml
services:
  labelgate:
    image: ghcr.io/channinghe/labelgate:v0.3
    container_name: labelgate
    restart: unless-stopped
    # use command "stat -c '%g' /var/run/docker.sock" to get the group id of the docker socket
    group_add:
      - "REPLACE_WITH_GROUP_ID"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      # Run the command "sudo chown -R 65532:65532 ./labelgate-data" to ensure the mount path has the correct ownership.
      - ./labelgate-data:/app/config
    environment:
      - LABELGATE_CLOUDFLARE_API_TOKEN
      - LABELGATE_CLOUDFLARE_ACCOUNT_ID
      - LABELGATE_CLOUDFLARE_TUNNEL_ID
      - LABELGATE_API_TOKEN
    # Dashboard and API, authenticated with LABELGATE_API_TOKEN.
    # Bound to localhost on purpose: put TLS, a VPN or Cloudflare Access in front before exposing it.
    ports:
      - "127.0.0.1:28111:8080"
    # labelgate no need to connect to the network "cloudflared-network"
    # Because Labelgate simply uses the Cloudflare API to create tunnel ingress rules or DNS records.
    network_mode: bridge

  cloudflared:
    image: cloudflare/cloudflared:latest
    restart: unless-stopped
    command: tunnel run --token ${TUNNEL_TOKEN}
    networks:
      - cloudflared-network

  webapp:
    image: nginx:alpine
    container_name: webapp
    labels:
      # Expose the app through the tunnel
      labelgate.tunnel.web.hostname: "app.example.com"
      labelgate.tunnel.web.service: "http://webapp:80"
      # A different hostname can use a plain DNS record instead.
      # Never use the same hostname for a tunnel and a DNS service: that is a conflict.
      # labelgate.dns.web.hostname: "direct.example.com"
      # labelgate.dns.web.target: "203.0.113.10"
    networks:
      - cloudflared-network

# Create a network for the services you want to connect to cloudflared.
# This allows your Cloudflare tunnel to connect to services via their container_name within the "cloudflared-network" bridge, eliminating the need for port mapping.
# Consolidating all public services into a single network ensures they remain isolated from private services.
networks:
  cloudflared-network:
```

```bash
docker compose up -d
```

That's it. Labelgate watches your containers and syncs labels to Cloudflare automatically. Open `http://localhost:28111/dashboard/` and sign in with your `LABELGATE_API_TOKEN` to see what it manages.

> **Important:** `LABELGATE_API_TOKEN` is required. Without it the API and the dashboard are disabled (Labelgate keeps syncing and logs an error). Never publish the port to the internet without TLS in front of it.
>
> If you change `api.base_path` (`LABELGATE_API_BASE_PATH`, default `/api`), the dashboard picks it up automatically: the server injects a `<meta name="labelgate-api-base">` tag into the dashboard page. Agent tokens (`connect.token`, `agent.accept_token`, per-agent `token`) must be at least 32 characters; generate them with `openssl rand -hex 32`. High entropy matters because a captured handshake can be attacked offline. Agent IDs are lower-case and may contain dots (`^[a-z0-9][a-z0-9._-]{0,62}$`), but a dotted ID cannot be used as a key under `agent.agents` in v0.3.0.

### Good to know

- **Ownership:** Labelgate only modifies and deletes Cloudflare resources it created (DNS records carry a `labelgate:<instance_id>` comment, Access apps a `labelgate-<instance_id>` tag, tunnel rules are tracked). Pre-existing records are never adopted: you get a `conflict` error instead, and manually managed tunnel ingress rules are preserved. (To hand a DNS record over deliberately, set its comment to `labelgate:<instance_id>` yourself; the ID is in the startup log.)
- **Cleanup:** `cleanup` defaults to `true`: resources it owns are removed 30 minutes (`sync.remove_delay`) after their container is gone. Set `cleanup: "false"` on a service to keep them.
- **Strict labels:** invalid label values and unknown properties are errors, shown in the dashboard and logs.
- **Agents:** use `wss://` and the `/ws` path (`wss://main.example.com:8081/ws`); TLS is verified by default.

Upgrading from v0.2? Read [Upgrading to v0.3](https://labelgate-docs.pages.dev/docs/upgrading) first.

> **Note:** We do **not** publish a `latest` tag. Use explicit version tags:
>
> | Tag | Description |
> |-----|-------------|
> | `v0.3.0` | Exact version |
> | `v0.3` | Latest patch in 0.3.x (recommended) |
> | `v0.3.0-full`, `v0.3-full` | debug variant includes busybox shell |
>
> Pre-release tags (for example `v0.3.0-rc1`) publish only the exact tag and its `-full` variant. The floating `v0` tag is not moved to 0.3; it stays on the 0.2.x line.

## Features

- [x] **DNS Management** — Create and sync Cloudflare DNS records via Docker labels
- [x] **Tunnel Ingress** — Expose services through Cloudflare Tunnels without port forwarding
- [x] **Zero Trust Access** — Configure Cloudflare Access policies declaratively
- [x] **Multi-host Agents** — Manage containers across multiple Docker hosts
- [x] **Safe by design** — Only touches Cloudflare resources it owns; never adopts or deletes yours
- [x] **Web Dashboard** — Built-in UI for monitoring (token-protected)
- [x] **Secure & Lightweight** — Rootless, distroless Docker images by default, with sizes typically under 30 MiB



## License

[MIT](LICENSE)
