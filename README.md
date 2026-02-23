
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

Create a `.env` file:

```bash
LABELGATE_CLOUDFLARE_API_TOKEN=your-api-token
LABELGATE_CLOUDFLARE_ACCOUNT_ID=your-account-id
LABELGATE_CLOUDFLARE_TUNNEL_ID=your-tunnel-id
# Authenticate cloudflared (not for labelgate)
TUNNEL_TOKEN=your-tunnel-token
```

Create `compose.yaml`:

```yaml
services:
  labelgate:
    image: ghcr.io/channinghe/labelgate:v0
    container_name: labelgate
    restart: unless-stopped
    # use command "stat -c '%g' /var/run/docker.sock" to get the group id of the docker socket
    group_add:
      - "REPLACE_WITH_GROUP_ID"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./labelgate-data:/app/config
    environment:
      - LABELGATE_CLOUDFLARE_API_TOKEN
      - LABELGATE_CLOUDFLARE_ACCOUNT_ID
      - LABELGATE_CLOUDFLARE_TUNNEL_ID
    ports:
      - "28111:8080"
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
      labelgate.tunnel.web.hostname: "app.example.com"
      labelgate.tunnel.web.service: "http://webapp:80"
      # or dns 
      labelgate.dns.web-dns.hostname: "app.example.com"
      labelgate.dns.web-dns.target: "xxx.xxx.xxx.xxx"
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

That's it. Labelgate watches your containers and syncs labels to Cloudflare automatically.

## Features

- [x] **DNS Management** — Create and sync Cloudflare DNS records via Docker labels
- [x] **Tunnel Ingress** — Expose services through Cloudflare Tunnels without port forwarding
- [x] **Zero Trust Access** — Configure Cloudflare Access policies declaratively
- [x] **Multi-host Agents** — Manage containers across multiple Docker hosts
- [x] **Web Dashboard** — Built-in UI for monitoring
- [x] **Secure & Lightweight** — Rootless, distroless Docker images by default, with sizes typically under 10 MiB



## License

[MIT](LICENSE)
