# Apps and discovery

Which apps appear, what the page shows and how to publish container apps. [Back to the README](../README.md).

## What qualifies

| Route | What qualifies | Ways to publish |
|---|---|---|
| A [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) | Any Service tailhome's node can reach. With an OAuth client, every Service in the tailnet. | `tailscale serve --service`, [DockTail and TSDProxy v3](#publish-container-apps) |
| An app on its own tailnet device | A device in this tailnet that answers HTTPS on port 443 with a valid certificate, or a TLS listener on a [configured preview port](setup.md#preview-ports) | `tailscale serve` on the device, [TSDProxy v2, tsbridge, ScaleTail](#publish-container-apps) |

tailhome checks only the [configured device ports](setup.md#preview-ports), not arbitrary ports. It does not read Docker labels or look behind reverse proxies. Apps at `nas:8080`, under subpaths or behind shared proxies need publishing through one of these routes. A `url` override changes where a tile points; it cannot add an app.

Your policy must let tailhome's node reach each app. Without credentials, the node uses your account's rules; with OAuth, grant access to `tag:tailhome` ([step 1](setup.md#set-up-oauth)).

### App on another port

An app at `nas:8080` appears once it is served at a tailnet name on port 443: the device's own name, or a Service's. With one app on a device, run this on the device, with the app's local port in place of `8080`:

```sh
tailscale serve --bg --https=443 8080
```

Tailscale serves the app at `https://<device>.<tailnet>.ts.net/` and keeps serving it after restarts; Docker or systemd must still keep the app running. The tile shows the device's name, title-cased; a `label` [override](customize.md) under that name renames it, and `icon` changes its icon.

With several apps on one device, give each its own [Tailscale Service](https://tailscale.com/docs/features/tailscale-services): one tile per Service. For development previews, use separate [preview ports and cards](#previews). Paths added with `--set-path` share one tile.

HTTPS certificates put the name of each device and Service served over HTTPS in public [Certificate Transparency](https://tailscale.com/docs/how-to/set-up-https-certificates) logs.

### Services

A Service becomes a tile if it advertises port 443 or 80, has a `url` or `web: true` override, or answers HTTP on its first TCP port. Other Services sit under Other. `web: false` keeps a Service under Other unless it has a `url` override.

### Devices

tailhome tries eligible devices in this tailnet on port 443 and any configured preview ports. Devices shared in from other tailnets, hidden devices and devices outside `TAILHOME_DEVICE_TAGS` are skipped. In this example, `devbox` is the publishing machine's host name; use your actual host name. A Service named `devbox` reserves only the device's port-443 entry, not `devbox:8443`.

Port-443 apps must answer HTTP at `/` after TLS verification; 401 or 404 counts as an answer. Once listed, an app stays visible when down until it leaves the tailnet or becomes ineligible. See [preview checks](#card-refresh-and-checks) for other ports.

tailhome forgets listed devices and cards when it restarts. Large tailnets can need several refreshes. It checks up to eight device ports at once, with five seconds for all checks on each port. It allows ten seconds to start checks on new ports, not to finish the whole refresh. Previously listed ports are checked first; slow ones can take roughly five seconds per batch of eight.

Set `TAILHOME_DEVICE_TAGS` to try only some devices, or `TAILHOME_DEVICES=off` to stop. A nonempty tag filter excludes untagged laptops; see [device identity and filtering](setup.md#optional-list-previews-in-tailhome).

### Devices with their own certificate

For a NAS or router with its own certificate, tag it, for example `tag:nas`, and set `TAILHOME_DEVICE_INSECURE_TLS_TAGS=tag:nas`. Until you do, the log names each device whose certificate tailhome can't verify.

Such a tile says "HTTPS certificate could not be verified", and your browser may warn when you open it. Fixing the certificate is better than clicking through.

Cards require a verified certificate even with an exception. A preview with an unverifiable certificate uses its default text or your [overrides](customize.md#overrides). Its certificate warning stays separate from the description.

## Previews

Previews appear above app groups. Each tile shows a title, description and host with its port, such as `devbox:8443` in the example above. A configured preview port needs no card: it defaults to `Devbox :8443` and "No description provided". A valid `preview: true` card can also make port 443 a preview, even when `TAILHOME_PREVIEW_PORTS` is empty. A title/description-only card on 443 remains an ordinary app. If a permanent app moves into Previews, [keep it under Apps with an override](troubleshooting.md#a-permanent-device-app-moved-into-previews).

Unreachable previews disappear from the page, JSON and shortcuts instead of showing red tiles. They do not expire automatically. A reachable Serve listener can keep a preview listed after its app stops. See [status rings](#using-the-page).

`set` groups versions on the same host; `version` shows a short badge instead of the icon. Set names are trimmed and case-sensitive. The same name on different hosts makes separate sets. A set keeps its heading with one version, and counts only listed versions. Each version needs its own app instance, port, card and URL. Badges do not identify an experiment or winner.

Private means access allowed by Tailscale policy, not visible only to you. Everyone who can open this tailhome sees every listed title, description, address and status, even when they cannot open the app. Use one shared launcher only for one metadata audience. Confidential projects need a separately restricted launcher whose catalog is also appropriate for that audience; narrower app grants alone do not hide catalog metadata. OAuth can list every Service. Hiding a preview affects everyone and does not revoke app access.

Your phone and colleagues need Tailscale access to the publishing machine and HTTPS port. For someone outside your tailnet, arrange an approved device share or invitation with your administrator. A link does not grant access, and this skill does not publish a public fallback. Keep the computer awake, connected and running the app. A background Serve mapping does not host or restart it.

The helper lists and cleans this OS account's recorded publications, not every team member's previews. Different machines can reuse IDs and comparison names. Separate accounts sharing one Tailscale daemon need external serialization or disjoint approved pools. See the [local state and lock details](../skills/tailnet-preview/references/serve.md#local-state-and-set-selectors).

Browsers treat different ports as different origins, but they share host-scoped cookies. Do not publish untrusted or sensitive previews on a hostname with valuable cookies.

### Preview cards

tailhome reads `/.well-known/tailhome.json` from each device's HTTPS address, not the home server. On a free port you have approved for previews, publish the app at `/` first, then its text card:

```sh
tailscale serve --bg --https=8443 --set-path=/ http://127.0.0.1:3000
tailscale serve --bg --https=8443 --set-path=/.well-known/tailhome.json 'text:{"title":"A · Short hero","description":"One CTA; pricing below the fold","icon":"shopify","preview":true,"set":"Landing redesign","version":"A"}'
```

This POSIX shell example keeps JSON quotes inside single quotes. Escape any apostrophes correctly when writing your own command. The [tailnet-preview skill](../skills/tailnet-preview/) uses JSON encoding and passes the card as one argument instead. Never put secrets, credentials or sensitive source paths in cards.

| Field | Type and limit |
|---|---|
| `title` | Optional plain-text string, at most 60 Unicode code points |
| `description` | Optional plain-text string, at most 140 code points; put what differs first |
| `icon` | Optional dashboard-icons slug matching `[a-z0-9-]+`, at most 64 characters; no URL or path |
| `preview` | Optional boolean; `true` marks this as a preview unless overridden |
| `set` | Optional plain-text string, at most 60 code points, scoped to this host |
| `version` | Optional one- or two-character ASCII alphanumeric badge, case preserved |

Return HTTP 200 with one non-null JSON object, valid UTF-8 and at most 4096 bytes. Only whitespace may follow it. If the first non-whitespace byte is not `{`, tailhome treats it as no card and logs nothing. This includes HTML from an app's catch-all route. Invalid bodies starting with `{` produce a `card request: invalid` log message, without repeating it on every check. Content-Type is ignored, so Serve's `text/plain; charset=utf-8` works. Duplicate keys, null or wrongly typed known fields, control characters and internal line or paragraph separators invalidate the whole card. Surrounding Unicode whitespace is trimmed before length checks. Empty strings act absent; unknown fields are ignored. `{}` clears old card fields. Cards cannot change URLs, access rules, order or app groups.

The text mount handles the card path and its slash-delimited descendants without reaching your app. Without it, card requests can reach the app and affect idle tracking. tailhome does not read HTML titles.

A `preview: true` card can reclassify a permanent app, including on 443. See [how to restore it to Apps](troubleshooting.md#a-permanent-device-app-moved-into-previews); title/description-only cards do not change its classification.

Before removing a preview you published manually, check that both mounts still belong to you. Then remove them:

```sh
tailscale serve --https=8443 --set-path=/.well-known/tailhome.json off
tailscale serve --https=8443 --set-path=/ off
```

Removing only `/` can leave the card and listener behind. Do not use whole-port `off` or `serve reset` on shared configuration. Let third-party publishing tools remove their own mappings. Serve `--bg` keeps mappings running, not your app process.

### Card refresh and checks

After successful TLS, tailhome reads cards when it first finds a preview, when it comes back after being unreachable or offline, and when you press refresh. It also tries cards on port 443 before deciding whether an app is a preview. Restarting repeats these first checks. Regular checks do not reread cards. Previews and extra ports get TLS handshakes without HTTP requests, even with `temporary: false`. Port-443 apps get HTTP checks at `/`. Services keep their HTTP/TCP checks and never read cards.

Refresh checks all reachable device ports, not just one preview. Clicks within ten seconds of the last refresh attempt, including scheduled attempts, skip checks. A skipped `POST /refresh` still redirects with 303 and leaves `updated` unchanged. If your preview is missing, wait at least eleven seconds and press refresh again. A 303 alone does not confirm it is listed: check `updated` and `error` in `/api/services.json`, then the exact URL and card text in `/api/list.json`.

A valid card replaces all saved card fields. HTTP 404/410, a non-object 200 body or an invalid card clears them. A network error or other HTTP status after you press refresh keeps the last valid card while TLS stays reachable. If the preview was unreachable, a failed card request clears old text, but can keep it marked as a preview to avoid requesting `/`. Cards are kept in memory. If you reuse a reachable preview port, press refresh to update its text.

On port 443, a failed first card request leaves tailhome unable to identify a preview, so it can request `/`. Device connections use current tailnet IPs and verify the device name. Card requests never use the Service client that accepts invalid certificates, or follow redirects.

The page shows the last check. Reload it to see changes. It also reloads after a refresh redirect or when you return to a page older than five minutes.

## Using the page

A tile opens the app. A row under Other copies what you need to connect: `ssh host` for port 22, `vnc://host` for 5900, a `postgres://`, `mysql://`, `redis://` or `mongodb://` URL for their default ports, else the hostname. A `copy` override sets the value.

The dot shows whether tailhome reached the app in its last check, not whether you can open it. Checks that only test TLS use a ring:

| Dot | Meaning |
|---|---|
| Green | An HTTP answer below 500, or a TCP connection for Other rows |
| Red | The check failed, or the device is offline. Hover for the reason. |
| Grey | Nothing to check, such as a Service with no TCP port |
| Green ring | HTTPS connection succeeded at last refresh; pages and app health not checked |
| Red ring | An app on an extra port is unreachable; previews are hidden instead |

tailhome checks from its own node, without following redirects. Service checks accept self-signed certificates; device checks verify them unless a tag exception applies.

tailhome refreshes every five minutes (`TAILHOME_REFRESH`). Select **refresh** to check now. If discovery fails, the page keeps the last list and shows the error.

The button after **add** switches the theme: auto follows your system, then light, then dark. The choice stays in that browser only, so other people who open the page keep their own.

On a phone, use Add to Home Screen (Safari) or Install app (Chrome). Type `home/go/jellyfin` in the address bar to open a tile by its Service or device name. `/go/devbox` means port 443; `/go/devbox:8443` means that exact port. Names ignore case; an encoded colon or trailing slash works. Hidden or unreachable previews return a 404 page with a home link, without saying they expired.

## Publish container apps

[DockTail](https://github.com/marvinvr/docktail) and [TSDProxy](https://github.com/almeidapaulopt/tsdproxy) v3 (`services: true`) publish containers as Services. TSDProxy v2, [tsbridge](https://github.com/jtdowney/tsbridge) and [ScaleTail](https://github.com/tailscale-dev/ScaleTail) give each app its own tailnet device. Both routes work.

| Topic | What to do |
|---|---|
| Groups | With OAuth, DockTail's default `tag:container` and TSDProxy's shared tags put every tile in one group. Set `docktail.tags` per container (each tag needs `tagOwners` and `autoApprovers` entries), or use `group` overrides. |
| Labels | These tools omit display names. Use `label` overrides. |
| Descriptions | With OAuth, `docktail.service.description` appears on the tile. |
| Paths | Tiles open `/`; the API omits `docktail.service.path`. Set `url` overrides. |
| Stopped containers | DockTail keeps their Services, leaving red tiles. Set `DELETE_UNUSED_SERVICES=true` in DockTail or `hidden: true`. TSDProxy deletes them. |
