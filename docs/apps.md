# Apps and discovery

Which apps appear, what the page shows and how to publish container apps. [Back to the README](../README.md).

## What qualifies

| Route | What qualifies | Ways to publish |
|---|---|---|
| A [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) | Any Service tailhome's node can reach. With an OAuth client, every Service in the tailnet. | `tailscale serve --service`, [DockTail and TSDProxy v3](#publish-container-apps) |
| An app on its own tailnet device | A device in this tailnet that answers `https://<device>.<tailnet>.ts.net/` on port 443 with a valid certificate | `tailscale serve` on the device, [TSDProxy v2, tsbridge, ScaleTail](#publish-container-apps) |

tailhome does not scan port ranges, read Docker labels or look behind reverse proxies. An app at `nas:8080`, under a sub-path or behind a shared proxy appears only once you publish it one of these two ways. A `url` override changes where a tile points; it cannot add an app.

Your policy must let tailhome's node reach each app. Without credentials, the node uses your account's rules; with OAuth, grant access to `tag:tailhome` ([step 1](setup.md#set-up-oauth)).

Nothing published yet? Start with [Tailscale Services](https://tailscale.com/docs/features/tailscale-services).

### Services

A Service becomes a tile if it advertises port 443 or 80, has a `url` or `web: true` override, or answers HTTP on its first TCP port. Other Services sit under Other. `web: false` keeps a Service under Other unless it has a `url` override.

### Devices

tailhome tries every device of this tailnet on each refresh and lists the ones that answer HTTPS on port 443 with a valid certificate; 401 or 404 counts as an answer. Devices shared in from other tailnets, `hidden` devices and devices named like a Service are skipped.

A listed device stays, red when offline, until it leaves the tailnet or becomes ineligible. tailhome forgets devices when it restarts; they return when they next answer. In a large tailnet, a device can take a few refreshes to appear.

Set `TAILHOME_DEVICE_TAGS` to try only some devices, or `TAILHOME_DEVICES=off` to stop.

### Devices with their own certificate

For a NAS or router with its own certificate, tag it, for example `tag:nas`, and set `TAILHOME_DEVICE_INSECURE_TLS_TAGS=tag:nas`. Until you do, the log names each device whose certificate tailhome can't verify.

Such a tile says "Unverified HTTPS", and your browser may warn when you open it. Fixing the certificate is better than clicking through.

## Using the page

A tile opens the app. A row under Other copies what you need to connect: `ssh host` for port 22, `vnc://host` for 5900, a `postgres://`, `mysql://`, `redis://` or `mongodb://` URL for their default ports, else the hostname. A `copy` override sets the value.

The dot shows whether tailhome reached the app in its last check, not whether you can open it:

| Dot | Meaning |
|---|---|
| Green | An HTTP answer below 500, or a TCP connection for Other rows |
| Red | The check failed, or the device is offline. Hover for the reason. |
| Grey | Nothing to check, such as a Service with no TCP port |

tailhome checks from its own node, without following redirects. Service checks accept self-signed certificates; device checks verify them unless a tag exception applies.

tailhome refreshes every five minutes (`TAILHOME_REFRESH`). Select **refresh** to check now. If discovery fails, the page keeps the last list and shows the error.

On a phone, use **Add to Home Screen** (Safari) or **Install app** (Chrome). Type `home/go/jellyfin` in the address bar to open a tile by its Service or device name.

## Publish container apps

[DockTail](https://github.com/marvinvr/docktail) and [TSDProxy](https://github.com/almeidapaulopt/tsdproxy) v3 (`services: true`) publish containers as Services. TSDProxy v2, [tsbridge](https://github.com/jtdowney/tsbridge) and [ScaleTail](https://github.com/tailscale-dev/ScaleTail) give each app its own tailnet device. Both routes work.

| Topic | What to do |
|---|---|
| Groups | With OAuth, DockTail's default `tag:container` and TSDProxy's shared tags put every tile in one group. Set `docktail.tags` per container (each tag needs `tagOwners` and `autoApprovers` entries), or use `group` overrides. |
| Labels | These tools omit display names. Use `label` overrides. |
| Descriptions | With OAuth, `docktail.service.description` appears on the tile. |
| Paths | Tiles open `/`; the API omits `docktail.service.path`. Set `url` overrides. |
| Stopped containers | DockTail keeps their Services, leaving red tiles. Set `DELETE_UNUSED_SERVICES=true` in DockTail or `hidden: true`. TSDProxy deletes them. |
