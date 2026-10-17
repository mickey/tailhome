<div align="center">

# tailhome

**The start page that builds itself from your [Tailscale Services](https://tailscale.com/docs/features/tailscale-services).**<br>
Type `http://home/` in your browser to see every Service on your tailnet, grouped, with icons and live health checks.

[Quick start](#quick-start) · [Customizing](#customizing) · [How it works](#how-it-works) · [Troubleshooting](#troubleshooting)

</div>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshot-dark.png">
  <img alt="tailhome in a browser at home/, showing Services grouped into Home Lab, Media, Monitoring and Other" src="docs/screenshot.png">
</picture>

- **Nothing to maintain.** Services come from the Tailscale API, groups from their tags, icons from [dashboard-icons](https://github.com/homarr-labs/dashboard-icons). Define a new Service and it shows up on the next refresh.
- **One binary, no Tailscale on the host.** tailhome joins your tailnet on its own through [tsnet](https://tailscale.com/docs/features/tsnet).
- **Real health checks.** Each Service is checked from inside the tailnet, so a green dot means you can actually reach it.

tailhome is a **launcher, not a dashboard**: no widgets, no feeds. For those, use [Homepage](https://gethomepage.dev) or [Glance](https://github.com/glanceapp/glance). To publish Docker containers as Services, see [With DockTail or TSDProxy](#with-docktail-or-tsdproxy).

---

## Quick start

> **Prerequisites:** admin access to your tailnet, Tailscale 1.86 or later on the devices you browse from, and **MagicDNS** plus **HTTPS Certificates** turned on in the [DNS settings](https://console.tailscale.com/admin/dns).

### 1. Set up Tailscale (once)

1. **Allow `tag:tailhome`** in your [access controls](https://console.tailscale.com/admin/acls):

   ```jsonc
   "tagOwners": {
     "tag:tailhome": ["autogroup:admin"]
   },
   "autoApprovers": {
     "services": { "svc:home": ["tag:tailhome"] }
   }
   ```

   With Tailscale's default policy (everyone reaches everything), that's all. With a stricter policy, also add:

   ```jsonc
   "grants": [
     // Who can open the start page
     { "src": ["autogroup:member"], "dst": ["svc:home"], "ip": ["*"] },
     // What tailhome may health-check
     { "src": ["tag:tailhome"], "dst": ["*"], "ip": ["*"] }
   ]
   ```

2. **Define the `home` Service:** on the [Services](https://console.tailscale.com/admin/services) page, select **Advertise → Define a Service**, name it `home`, enter the ports `443,80`, leave the tags empty, and select **Define Service**.

3. **Create an OAuth client:** in [Trust credentials](https://console.tailscale.com/admin/settings/trust-credentials), add a credential with the custom scopes **General → Services → Read**, to list your Services, and **Keys → Auth Keys → Write** with the tag `tag:tailhome`, to join the tailnet. Copy the client ID and secret: the secret is shown only once.

### 2. Run tailhome

Put the credentials in a `.env` file (see [`.env.example`](.env.example)):

```sh
TAILHOME_CLIENT_ID='your-client-id'
TAILHOME_CLIENT_SECRET='tskey-client-your-secret'
```

Next to it, create this [`compose.yaml`](compose.yaml):

```yaml
services:
  tailhome:
    image: ghcr.io/mickey/tailhome:latest
    env_file: .env
    environment:
      TZ: Etc/UTC # your time zone, for the "updated" time
    volumes:
      - tailhome-data:/data
    restart: unless-stopped

volumes:
  tailhome-data:
```

```sh
docker compose up -d
```

Once `docker compose logs` shows `serving https://home.<tailnet>.ts.net`, open `http://home/` from any device on your tailnet. Keep the trailing slash, or your browser may search for "home".

<details>
<summary><strong>Without Docker: binary, Go or systemd</strong></summary>

Download a binary from [Releases](https://github.com/mickey/tailhome/releases), or build one with Go 1.27.2 or later:

```sh
go install github.com/mickey/tailhome@latest

TAILHOME_CLIENT_ID='your-client-id' \
TAILHOME_CLIENT_SECRET='tskey-client-your-secret' \
tailhome
```

For systemd, as root:

```sh
install -m 755 tailhome /usr/local/bin/
install -d /etc/tailhome
install -m 600 .env /etc/tailhome/env
install -m 644 contrib/tailhome.service /etc/systemd/system/
systemctl enable --now tailhome
```

The unit keeps state in `/var/lib/tailhome` and reads overrides from `/etc/tailhome/overrides.yaml`.

</details>

> [!NOTE]
> Keep the state directory (`/data` in Docker) and the OAuth client. Without the state, tailhome joins as a new node and the old one stays in the admin console. Without the client, it can't list Services.

---

## Customizing

Out of the box, tailhome takes everything from Tailscale:

| Field | Source |
|---|---|
| Label | The Service's display name, or its name in title case |
| Description | The Service's description |
| Group | Its first tag: `tag:home-lab` becomes "Home Lab". Untagged Services go under "Services". |
| Icon | The [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) entry named after the Service, or the label's first letter |

To change that, write an `overrides.yaml`. Every field is optional, and [`overrides.example.yaml`](overrides.example.yaml) lists them all:

```yaml
jellyfin:
  group: Media
  order: -1                 # lower sorts first; unset counts as 0, ties sort by label
grafana:
  label: Grafana
  icon: grafana             # a dashboard-icons name, or a full image URL
  url: https://grafana.example.ts.net/d/main
nas:
  url: vnc://nas.example.ts.net:5900   # also ssh, rdp, smb, ftp, sftp, telnet
postgres:
  hidden: true
```

In Docker, copy it into the volume with `docker compose cp overrides.yaml tailhome:/data/`; the file must be readable by everyone (`chmod 644`). The binary reads it from the directory you start it in. Set `TAILHOME_OVERRIDES` to put it anywhere else.

tailhome rereads the file on every refresh. Keys are Service names, with or without `svc:`. An unknown field makes the refresh fail. A key that matches no Service is ignored, with a warning in the logs.

### With DockTail or TSDProxy

tailhome lists Services, not devices. [DockTail](https://github.com/marvinvr/docktail) and [TSDProxy](https://github.com/almeidapaulopt/tsdproxy) v3 (`services: true`) publish containers as Services. TSDProxy v2, [tsbridge](https://github.com/jtdowney/tsbridge) and [ScaleTail](https://github.com/tailscale-dev/ScaleTail) create one device per app, which tailhome doesn't see.

- **Groups.** DockTail tags Services `tag:container` by default and TSDProxy gives them all the same tags, so they share one group. Set `docktail.tags` per container (each tag needs `tagOwners` and `autoApprovers` entries), or `group` overrides.
- **Labels.** These tools set no display name and may clear one set in the admin console: use `label` overrides.
- **Descriptions.** `docktail.service.description` shows on the tile.
- **Paths.** The tile opens `/`, since the API doesn't expose `docktail.service.path`: set `url`.
- **Stopped containers.** DockTail keeps their Service, so the tile stays red: set `DELETE_UNUSED_SERVICES=true` in DockTail, or `hidden: true`. TSDProxy deletes it.

---

## How it works

- **Tiles and "Other".** A Service becomes a clickable tile when it listens on port 443 or 80, has a `url` or `web: true` override, or answers HTTP on its first port. A Service without a usable port (such as `do-not-validate`) is tried on 443, then 80. A Service that is down when tailhome starts waits under "Other" until it first answers. Everything else, such as a database, goes under "Other": click it to copy its hostname.
- **Health.**
  - **Green:** an HTTP answer below 500, redirects included. For "Other" and for non-HTTP links (`ssh://`, `vnc://`), a TCP connection that succeeds.
  - **Red:** the check failed. Hover the dot to see why. A tile that goes down stays in its group.
  - **Grey:** nothing to check, such as a Service with no TCP port.
- **Refresh.** tailhome reloads the list and rechecks every Service every 5 minutes. The **refresh** link does it now, at most once every 10 seconds. If a refresh fails, because of the Tailscale API or the overrides file, the page keeps the last list and shows the error.
- **JSON.** `/api/services.json` returns the same data as the page.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `TAILHOME_CLIENT_ID` | required | OAuth client ID |
| `TAILHOME_CLIENT_SECRET` | required | OAuth client secret |
| `TAILHOME_STATE_DIR` | `/data` in Docker, else `tsnet-tailhome` in your user config dir | Where tailhome keeps its tailnet identity |
| `TAILHOME_OVERRIDES` | `/data/overrides.yaml` in Docker, else `overrides.yaml` | Path to the overrides file |
| `TAILHOME_REFRESH` | `5m` | How often to reload and recheck Services |
| `TAILHOME_SERVICE` | `svc:home` | Service that serves the page |
| `TAILHOME_TAG` | `tag:tailhome` | Tag of the tailhome node |
| `TAILHOME_HOSTNAME` | `tailhome` | Node name in the tailnet |
| `TAILHOME_TAILNET` | the OAuth client's tailnet | Tailnet to list |
| `TS_AUTHKEY` | unset | Auth key to join with, instead of minting one. It must allow `tag:tailhome`. |
| `TS_NO_LOGS_NO_SUPPORT` | unset | Set to `true` to stop uploading tsnet diagnostic logs to Tailscale |

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `home/` doesn't load | `svc:home` has no approved host: define it (step 2), add the `autoApprovers` entry, or approve the host on the Services page. tailhome logs a warning when `svc:home` isn't defined. |
| `home/` doesn't resolve on one device | MagicDNS is off, or the device runs Tailscale older than 1.86. On Linux before 1.94, run `sudo tailscale set --accept-routes`. |
| `https://home/` won't connect | HTTPS works on the full name only. Open `http://home/`, which redirects there. |
| `https://home.<tailnet>.ts.net/` won't connect | HTTPS Certificates are off in the DNS settings. |
| The first HTTPS load is slow or times out | The certificate is being issued. Retry in a minute. |
| tailhome exits with `joining the tailnet: creating an auth key for tag:tailhome: …` | The OAuth client lacks Auth Keys: Write for `tag:tailhome`, or the tag is missing from `tagOwners`. |
| "Refresh failed: …" mentioning `oauth2` or `403` | The OAuth client lacks the Services: Read scope. |
| A dot is red but the Service opens fine | Your policy doesn't let `tag:tailhome` reach that Service: add the grants from step 1. |

---

## Security and privacy

- **Service names.** Anyone who can open `svc:home` sees every Service name, on the page and in `/api/services.json`, including Services they can't open. Restrict who reaches `svc:home` if that matters.
- **Icons.** Browsers load icons from `cdn.jsdelivr.net`, which reveals Service names to jsDelivr. Set `icon` overrides to your own URLs to avoid it.
- **Logs.** Like any Tailscale node, tailhome uploads diagnostic logs to Tailscale. Set `TS_NO_LOGS_NO_SUPPORT=true` to turn that off.

Report vulnerabilities as described in [SECURITY.md](SECURITY.md). To contribute, see [CONTRIBUTING.md](CONTRIBUTING.md). tailhome is released under the [MIT license](LICENSE).
