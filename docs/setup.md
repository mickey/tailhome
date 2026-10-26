# Set up tailhome

[Back to the README](../README.md). Start with [Docker Compose and prerequisites](../README.md#quick-start), or choose an alternative below.

## Docker run

Without a compose file, run `docker run -d --name tailhome --restart unless-stopped -v tailhome-data:/data ghcr.io/mickey/tailhome:latest`, then `docker logs -f tailhome`.

Follow the [first-start steps](../README.md#quick-start). For unattended login, set `TS_AUTHKEY` to an [auth key](https://console.tailscale.com/admin/settings/keys). A tagged key avoids key expiry and needs a `tagOwners` entry. Existing nodes keep their identity; the key applies only when joining.

## Binary and Go

Download a binary from [Releases](https://github.com/mickey/tailhome/releases), or install it with Go 1.27.3 or later:

```sh
go install github.com/mickey/tailhome@latest
tailhome
```

`go install` puts the binary in `$(go env GOPATH)/bin`; add that directory to your `PATH`.

Follow the [first-start steps](../README.md#quick-start).

## systemd

Run these commands as root from a repository checkout; release archives do not include the systemd unit:

```sh
install -m 755 tailhome /usr/local/bin/
install -d /etc/tailhome
install -m 644 contrib/tailhome.service /etc/systemd/system/
systemctl enable --now tailhome
```

The unit runs tailhome as a dynamic user, stores state in `/var/lib/tailhome` and reads `/etc/tailhome/overrides.yaml`. It also reads `/etc/tailhome/env` if present, one `KEY=value` per line, for any setting such as `TS_AUTHKEY` or OAuth credentials. Use `journalctl -u tailhome` to find the login link.

## Set up OAuth

An optional OAuth client adds Service groups from tags and descriptions from the admin console. It serves the page at `svc:home` instead of the node name `home`. Setup needs admin access:

1. Allow `tag:tailhome` in your [access controls](https://console.tailscale.com/admin/acls):

   ```jsonc
   "tagOwners": {
     "tag:tailhome": ["autogroup:admin"]
   },
   "autoApprovers": {
     "services": { "svc:home": ["tag:tailhome"] }
   }
   ```

   Tailscale's default policy allows everyone to reach everything. With a stricter policy, also add:

   ```jsonc
   "grants": [
     // Who can open the start page
     { "src": ["autogroup:member"], "dst": ["svc:home"], "ip": ["*"] },
     // What tailhome may health-check
     { "src": ["tag:tailhome"], "dst": ["*"], "ip": ["*"] }
   ]
   ```

2. Rename or remove any device named `home`, including a tailhome node that joined without credentials; the Service needs that name.

   Define the `home` Service: on the [Services](https://console.tailscale.com/admin/services) page, select **Advertise**, then **Define a Service**; name it `home`, enter `tcp:443` and `tcp:80`, leave tags empty, and select **Add service**.

3. Create an OAuth client. In [Trust credentials](https://console.tailscale.com/admin/settings/trust-credentials), add a credential with custom scopes **General → Services → Read** to list Services and **Keys → Auth Keys → Write** with `tag:tailhome` to join the tailnet.

   Copy the client ID and secret. Tailscale shows the secret only once.

4. Save the credentials in `.env` next to `compose.yaml`, replacing both placeholders, then restart:

   ```sh
   TAILHOME_CLIENT_ID='your-client-id'
   TAILHOME_CLIENT_SECRET='tskey-client-your-secret'
   ```

   If tailhome already joined without credentials, it must join again with its tag. Stop it and delete `tailscaled.state` from the state directory (`/data` in Docker).

   Remove the old `home` node from the admin console.

Without Docker, set the same two variables before starting tailhome.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `TAILHOME_CLIENT_ID` | unset | [OAuth client ID](setup.md#set-up-oauth). Set both credentials or neither. |
| `TAILHOME_CLIENT_SECRET` | unset | OAuth client secret |
| `TAILHOME_STATE_DIR` | `/data` in Docker, else `tsnet-tailhome` in your user config dir | Where tailhome keeps its tailnet identity |
| `TAILHOME_OVERRIDES` | `/config/overrides.yaml` with the [compose file](../compose.yaml), `/data/overrides.yaml` in the bare image, else `overrides.yaml` | Path to the [overrides file](customize.md#overrides) |
| `TAILHOME_REFRESH` | `5m` | Refresh interval; any positive Go duration |
| `TAILHOME_DEVICES` | `on` | Device discovery: `on` or `off` |
| `TAILHOME_DEVICE_TAGS` | empty | Discover devices matching any listed tag; empty matches all |
| `TAILHOME_DEVICE_INSECURE_TLS_TAGS` | empty | [Certificate exceptions](apps.md#devices-with-their-own-certificate) for devices matching any listed tag |
| `TAILHOME_SERVICE` | `svc:home` | Service that serves the page (OAuth only) |
| `TAILHOME_TAG` | `tag:tailhome` | Node tag (OAuth only) |
| `TAILHOME_HOSTNAME` | `home`, or `tailhome` with OAuth | Node name in the tailnet |
| `TAILHOME_TAILNET` | the OAuth client's tailnet | Tailnet to list (OAuth only) |
| `TS_AUTHKEY` | unset | Auth key for [unattended login](setup.md#docker-run). With OAuth, it must allow `TAILHOME_TAG` (default `tag:tailhome`). |
| `TS_NO_LOGS_NO_SUPPORT` | unset | See [Security and privacy](../README.md#security-and-privacy) |

Separate tags with commas or whitespace; use names such as `tag:nas`. Restart tailhome after changing environment variables. `tailhome -version` prints the version.
