<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/icon-light.svg" />
    <source media="(prefers-color-scheme: light)" srcset="assets/icon-dark.svg" />
    <img alt="Labelgate" src="assets/icon-dark.svg" width="48" height="48" />
  </picture>
</p>

<h1 align="center">Labelgate</h1>

<p align="center">
  Manage Cloudflare DNS, Tunnels and Zero Trust Access with Docker labels.
</p>

---

Add labels to a container, and Labelgate creates the Cloudflare DNS record, tunnel route or Access rule for it. When the container goes away, Labelgate cleans up. It never touches records you created by hand.

- **DNS records:** A, AAAA, CNAME, TXT, MX, SRV, CAA
- **Tunnel routes:** publish apps without opening ports
- **Access:** limit who can reach an app
- **Several hosts:** run a small agent on each extra Docker host
- **Dashboard:** see what is managed and what failed
- **Small and rootless:** distroless image, usually under 30 MiB

## Quick start

1. Create a `.env` file (keep it out of version control):

   ```bash
   LABELGATE_CLOUDFLARE_API_TOKEN=your-api-token
   LABELGATE_CLOUDFLARE_ACCOUNT_ID=your-account-id
   LABELGATE_CLOUDFLARE_TUNNEL_ID=your-tunnel-id
   LABELGATE_API_TOKEN=output-of-openssl-rand-hex-32   # dashboard login
   TUNNEL_TOKEN=your-tunnel-token                      # for cloudflared
   ```

2. Prepare the data folder and find the Docker socket's group:

   ```bash
   mkdir -p labelgate-data && sudo chown -R 65532:65532 labelgate-data
   stat -c '%g' /var/run/docker.sock
   ```

3. Create `compose.yaml`:

   ```yaml
   services:
     labelgate:
       image: ghcr.io/channinghe/labelgate:v0.3
       restart: unless-stopped
       group_add:
         - "REPLACE_WITH_GROUP_ID"   # output of the stat command
       volumes:
         - /var/run/docker.sock:/var/run/docker.sock:ro
         - ./labelgate-data:/app/config
       environment:
         - LABELGATE_CLOUDFLARE_API_TOKEN
         - LABELGATE_CLOUDFLARE_ACCOUNT_ID
         - LABELGATE_CLOUDFLARE_TUNNEL_ID
         - LABELGATE_API_TOKEN
       ports:
         - "127.0.0.1:8080:8080"     # dashboard, local only

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
       networks:
         - cloudflared-network

   networks:
     cloudflared-network:
       name: cloudflared-network
   ```

4. Start it:

   ```bash
   docker compose up -d
   ```

Open `https://app.example.com`. The dashboard is at `http://localhost:8080/dashboard/`; sign in with your `LABELGATE_API_TOKEN`.

Use the `v0.3` image tag. There is no `latest` tag.

## Documentation

Full docs: [labelgate-docs.pages.dev](https://labelgate-docs.pages.dev/)

- [Getting Started](https://labelgate-docs.pages.dev/docs/getting-started)
- [Label Reference](https://labelgate-docs.pages.dev/docs/labels)
- [Configuration](https://labelgate-docs.pages.dev/docs/configuration)
- [Ownership & Conflicts](https://labelgate-docs.pages.dev/docs/ownership)
- [Upgrading from v0.2](https://labelgate-docs.pages.dev/docs/upgrading)

## License

[MIT](LICENSE)
