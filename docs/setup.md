# Set up tailhome

[Back to the README](../README.md).

## Before you start

Enable MagicDNS and HTTPS Certificates in [Tailscale DNS settings](https://console.tailscale.com/admin/dns). Viewers need Tailscale; Service viewers need version 1.86 or later, plus `sudo tailscale set --accept-routes` on Linux before 1.94.

App hosts need Tailscale; tailhome's Docker host does not. Keep the state volume to preserve tailhome's identity and [disable key expiry](https://tailscale.com/docs/features/access-control/key-expiry).

## Docker Compose

Save [`compose.yaml`](../compose.yaml). With Compose 2.24.0 or later, run:

```sh
mkdir -p config
docker compose up -d
docker compose logs -f
```

Open the login link yourself. Whoever opens it chooses tailhome's tailnet and, with OAuth, can see your Service list there; keep it private.

When ready, open `http://home/` from a tailnet device, including the trailing slash. If logs show another name, use that.

Use `.env` for settings and `config/overrides.yaml` for [tile overrides](customize.md#overrides).

## Docker run

```sh
docker run -d --name tailhome --restart unless-stopped -v tailhome-data:/data ghcr.io/mickey/tailhome:latest
docker logs -f tailhome
```

Follow the [Compose login steps](#docker-compose). For unattended login, set `TS_AUTHKEY` to an [auth key](https://tailscale.com/docs/features/access-control/auth-keys).

## Binary and Go

Download a [release binary](https://github.com/mickey/tailhome/releases), or use Go 1.27.3 or later:

```sh
go install github.com/mickey/tailhome@latest
tailhome
```

Add `$(go env GOPATH)/bin` to `PATH` if needed. Follow the [Compose login steps](#docker-compose).

## Set up OAuth

OAuth adds Service groups and descriptions, and serves home through `svc:home`. You need tailnet admin access.

1. Add to your [access controls](https://console.tailscale.com/admin/acls):

   ```jsonc
   "tagOwners": { "tag:tailhome": ["autogroup:admin"] },
   "autoApprovers": { "services": { "svc:home": ["tag:tailhome"] } }
   ```

   Grant viewer-to-home, home-to-app and viewer-to-app access separately. Review [catalog visibility](#security-and-network-requests).

2. Stop tailhome. Rename or remove any device named `home`, then [define a Service](https://tailscale.com/docs/features/tailscale-services) named `home` with `tcp:443` and `tcp:80`, leaving tags empty.

3. In [Trust credentials](https://console.tailscale.com/admin/settings/trust-credentials), create an OAuth client with General → Services → Read. Save its ID and secret; the secret appears once.

4. Save these in `.env` next to `compose.yaml`:

   ```sh
   TAILHOME_CLIENT_ID=your-client-id
   TAILHOME_CLIENT_SECRET=tskey-client-your-secret
   ```

   If tailhome previously joined without OAuth, stop it and delete only `tailscaled.state`:

   ```sh
   docker compose stop
   docker run --rm --volumes-from "$(docker compose ps --all --quiet tailhome)" busybox rm -f /data/tailscaled.state
   ```

   Remove its old node from [Machines](https://console.tailscale.com/admin/machines) if it previously joined.

5. Run `docker compose up -d`, then open the new login link as a tailnet admin; approve the machine if required.

For `docker run`, use `--volumes-from tailhome` when resetting state and recreate with `--env-file .env`; write values without quotes. For binaries, set the same variables and reset `tailscaled.state` in the state directory before restarting.

Optional Keys → Auth Keys → Write for `tag:tailhome` enables automatic enrollment. A leaked secret with this scope can add devices that may serve home; Services: Read alone only reads Services.

## Preview ports

`TAILHOME_PREVIEW_PORTS=8443-8452,10000` adds HTTPS discovery ports. Port 443 is always checked; add at most 32 others, between 1 and 65535, excluding 443.

### Publish a direct preview

Follow the skill's [requirements](../skills/tailnet-preview/README.md#requirements) and [installation](../skills/tailnet-preview/README.md#install). Have the machine's owner approve publishing ports.

Complete any Serve consent or permission setup yourself, then ask the agent to retry. Agents must not sign in, elevate privileges or change policy.

### Optional: list previews in tailhome

Set `TAILHOME_PREVIEW_PORTS` in `.env`, then run `docker compose up -d`. Discovery ports do not approve publishing or grant access.

Leave `TAILHOME_DEVICE_TAGS` empty for personal laptops. Tagging replaces their user identity and changes their policy.

## systemd

From a repository checkout with the Linux binary at `./tailhome`, run as root: `install -m 755 tailhome /usr/local/bin/; install -d /etc/tailhome; install -m 644 contrib/tailhome.service /etc/systemd/system/; systemctl enable --now tailhome`.
Put settings in `/etc/tailhome/env`; state lives in `/var/lib/tailhome`. Find the login link with `journalctl -u tailhome`.
Put overrides in `/etc/tailhome/overrides.yaml`, readable by the service user.

## Environment variables

Recreate containers or restart binaries after changes. Separate tags with commas or whitespace.

<details>
<summary>All environment variables</summary>

| Variable | Default | Purpose |
|---|---|---|
| `TAILHOME_CLIENT_ID` | unset | [OAuth](#set-up-oauth) ID; set both credentials |
| `TAILHOME_CLIENT_SECRET` | unset | OAuth secret |
| `TAILHOME_STATE_DIR` | Docker: `/data`; otherwise user config `tsnet-tailhome` | Identity storage |
| `TAILHOME_OVERRIDES` | Compose: `/config/overrides.yaml`; image: `/data/overrides.yaml`; binary: `overrides.yaml` | [Tile overrides](customize.md#overrides) |
| `TAILHOME_REFRESH` | `5m` | Refresh interval; positive Go duration |
| `TAILHOME_DEVICES` | `on` | `off` disables device discovery |
| `TAILHOME_ICONS` | `on` | `off` disables CDN icons |
| `TAILHOME_DEVICE_TAGS` | empty | Match any listed tag; empty includes untagged devices |
| `TAILHOME_DEVICE_INSECURE_TLS_TAGS` | empty | [Certificate exceptions](apps.md#devices-with-their-own-certificate) by tag |
| `TAILHOME_PREVIEW_PORTS` | empty | [Extra discovery ports](#preview-ports) |
| `TAILHOME_SERVICE` | `svc:home` | OAuth Service name |
| `TAILHOME_TAG` | `tag:tailhome` | OAuth node tag |
| `TAILHOME_HOSTNAME` | `home`; OAuth: `tailhome` | Node name |
| `TAILHOME_TAILNET` | OAuth client's tailnet | Tailnet to list |
| `TS_AUTHKEY` | unset | Enrollment key; OAuth requires `TAILHOME_TAG` permission |
| `TS_NO_LOGS_NO_SUPPORT` | unset | `true` disables Tailscale diagnostic uploads |

</details>

## Security and network requests

[Tailscale policy](https://tailscale.com/docs/features/access-control) controls access; tailhome has no separate login. Everyone who can open it sees the same catalog, including metadata for apps they cannot access.

- Keep OAuth secrets, auth keys, `tailscaled.state` and login links private.
- Discovery contacts reachable devices, including laptops and phones. Use `TAILHOME_DEVICES=off` to disable it.
- Icons contact `cdn.jsdelivr.net`, revealing icon names. Set `TAILHOME_ICONS=off` to stop this.
- Tailscale receives node connections and diagnostic uploads; OAuth contacts its API.
- HTTPS names appear in public Certificate Transparency logs. Preview cards must not contain secrets or sensitive paths.
- Cookies are shared across ports, including `HttpOnly` cookies. Do not publish untrusted code on a hostname where apps keep you signed in.

See [preview privacy](apps.md#previews) and [SECURITY.md](../SECURITY.md).
