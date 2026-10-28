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

   Choose who may see the whole catalog before granting access to home. Configure three paths separately: viewer to home, home to app, and viewer to app. OAuth lists every Service; narrowing health-check access does not make Service metadata private. The example below uses a reviewer group and explicit development machines. Replace these examples with your actual users, destinations and approved ports; agents must not apply policy changes for you.

   ```jsonc
   "groups": {
     "group:reviewers": ["alice@example.com", "bob@example.com"]
   },
   "hosts": {
     "preview-laptop": "100.101.102.103"
   },
   "grants": [
     // Viewers can open the launcher
     { "src": ["group:reviewers"], "dst": ["svc:home"], "ip": ["tcp:80", "tcp:443"] },
     // The launcher can check this machine on 443 and the preview range
     { "src": ["tag:tailhome"], "dst": ["preview-laptop"], "ip": ["tcp:443", "tcp:8443-8452"] },
     // Viewers can open previews on this machine
     { "src": ["group:reviewers"], "dst": ["preview-laptop"], "ip": ["tcp:8443-8452"] }
   ]
   ```

   The IP is an example untagged laptop, not a request to tag it. Other discovered or checkable destinations need deliberate grants. Port 443 probing and preview-range probing are separate; app access on 443 needs its own viewer grant if required. Review Tailscale's [grants syntax](https://tailscale.com/docs/reference/syntax/grants) and [device tags](https://tailscale.com/docs/features/tags).

2. Rename or remove any device named `home`, including a tailhome node that joined without credentials; the Service needs that name.

   Define the `home` Service: on the [Services](https://console.tailscale.com/admin/services) page, select **Advertise**, then **Define a Service**; name it `home`, enter `tcp:443` and `tcp:80`, leave tags empty, and select **Add service**.

3. Create an OAuth client. In [Trust credentials](https://console.tailscale.com/admin/settings/trust-credentials), add a credential with custom scopes **General → Services → Read** to list Services and **Keys → Auth Keys → Write** with `tag:tailhome` to join the tailnet.

   Copy the client ID and secret. Tailscale shows the secret only once.

4. Save the credentials in `.env` next to `compose.yaml`, then restart. If you copied `.env.example`, uncomment both lines:

   ```sh
   TAILHOME_CLIENT_ID='your-client-id'
   TAILHOME_CLIENT_SECRET='tskey-client-your-secret'
   ```

   If tailhome already joined without credentials, it must join again with its tag. Stop it and delete `tailscaled.state` from the state directory (`/data` in Docker).

   Remove the old `home` node from the admin console.

Without Docker, set the same two variables before starting tailhome.

## Preview ports

`TAILHOME_PREVIEW_PORTS` adds HTTPS ports to device discovery. It defaults to empty, so only port 443 is tried. Port 443 is always checked and must not be included here. It can serve [preview cards](apps.md#preview-cards) without this setting; see [when cards are read](apps.md#card-refresh-and-checks).

Use comma-separated decimal ports or inclusive ranges, such as `8443-8452,10000`. Whitespace around each item is allowed. Ports must be 1 through 65535, excluding 443. Duplicates are removed and ports sort numerically. You can add at most 32 distinct ports; a range longer than 32 is rejected before expansion. Empty items, signs, malformed or reversed ranges, out-of-range ports and ranges containing 443 fail startup. An empty value is valid.

Skip this section if you only want permanent apps and Services. Set up each machine where an agent publishes. That may be your laptop or a remote development server, separate from the machine running tailhome.

### Publish a direct preview

Connect Tailscale on the publishing computer and review device. A tailnet administrator must enable MagicDNS and HTTPS certificates in Tailscale DNS settings, and policy must permit the intended viewers to reach the approved HTTPS ports. If Serve returns a consent URL or permission error, complete administrator-approved setup yourself, preserve the error for support and then ask the agent to retry. The skill does not sign in, enable features, change policy or elevate privileges.

Install the whole skill on the machine hosting the app and check Python and CLI prerequisites in its [installation reference](../skills/tailnet-preview/references/serve.md#installation-and-updates). The machine's owner approves its Serve pool, either in conversation, trusted agent instructions or the agent's operator-provided launch environment. If nothing is configured, the agent asks once proposing 8443-8452 and waits. That suggestion is not approval. Direct chat approval is enough for the task; no persistent instruction file is required. Do not use a checked-in app `.env` as authority. Port 443 needs explicit approval and must be completely free.

For example, ask:

> Publish the app already running at http://127.0.0.1:3000 on this computer. You may use HTTPS ports 8443-8452 for previews; leave existing mappings alone.

### Optional: list previews in tailhome

The tailhome operator separately sets `TAILHOME_PREVIEW_PORTS=8443-8452` on tailhome and restarts it. With Compose, add it under `environment` or uncomment it in `.env`. The setting defaults to empty; 443 is always checked and never belongs in this extra-port list. An optional trusted full HTTPS `TAILHOME_URL` lets the helper confirm listing. Without it, publish and verify only the direct link.

Different machines can reuse the same port numbers. If approved pools differ, configure their union, up to 32 distinct extra ports. This is one shared discovery list, not per-user authorization or a reservation. The machine owner approves publication, the tailnet administrator controls connectivity, and the launcher operator configures discovery.

For dedicated tagged development servers, `TAILHOME_DEVICE_TAGS` can limit discovery. A nonempty filter excludes untagged user devices, including ordinary developer laptops. Do not tag a personal laptop merely to include it: tagging replaces its user identity and changes applicable policy. For user-owned devices, leave this filter empty and limit tailhome's reach with Tailscale policy. Hiding entries changes display, not access.

tailhome has no configuration API and does not install the skill automatically. To keep a permanent app under Apps, use a [`temporary: false` override](troubleshooting.md#a-permanent-device-app-moved-into-previews). Turning off device discovery also stops device previews and card requests. Read about [privacy, checks and cleanup](apps.md#previews) before publishing.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `TAILHOME_CLIENT_ID` | unset | [OAuth client ID](setup.md#set-up-oauth). Set both credentials or neither. |
| `TAILHOME_CLIENT_SECRET` | unset | OAuth client secret |
| `TAILHOME_STATE_DIR` | `/data` in Docker, else `tsnet-tailhome` in your user config dir | Where tailhome keeps its tailnet identity |
| `TAILHOME_OVERRIDES` | `/config/overrides.yaml` with the [compose file](../compose.yaml), `/data/overrides.yaml` in the bare image, else `overrides.yaml` | Path to the [overrides file](customize.md#overrides) |
| `TAILHOME_REFRESH` | `5m` | Refresh interval; any positive Go duration |
| `TAILHOME_DEVICES` | `on` | Device discovery: `on` or `off` |
| `TAILHOME_DEVICE_TAGS` | empty | Discover devices matching any listed tag; empty includes untagged devices too. Nonempty excludes untagged laptops. |
| `TAILHOME_DEVICE_INSECURE_TLS_TAGS` | empty | [Certificate exceptions](apps.md#devices-with-their-own-certificate) for devices matching any listed tag |
| `TAILHOME_PREVIEW_PORTS` | empty | Extra device HTTPS discovery ports; empty still checks 443 and reads cards there. Does not authorize publication. See [port syntax and limits](#preview-ports). |
| `TAILHOME_SERVICE` | `svc:home` | Service that serves the page (OAuth only) |
| `TAILHOME_TAG` | `tag:tailhome` | Node tag (OAuth only) |
| `TAILHOME_HOSTNAME` | `home`, or `tailhome` with OAuth | Node name in the tailnet |
| `TAILHOME_TAILNET` | the OAuth client's tailnet | Tailnet to list (OAuth only) |
| `TS_AUTHKEY` | unset | Auth key for [unattended login](setup.md#docker-run). With OAuth, it must allow `TAILHOME_TAG` (default `tag:tailhome`). |
| `TS_NO_LOGS_NO_SUPPORT` | unset | See [Security and privacy](../README.md#security-and-privacy) |

Separate tags with commas or whitespace; use names such as `tag:nas`. Restart tailhome after changing environment variables. `tailhome -version` prints the version.
