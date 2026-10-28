# Troubleshooting

[Back to the README](../README.md).

| Symptom | Cause and next step |
|---|---|
| The page lists no apps | Allow tailhome's node to reach your Services and devices. If Services stay missing, [set up OAuth](setup.md#set-up-oauth) to list them through the API. |
| A device app doesn't appear | Check the [device discovery rules](apps.md#devices), port access and [certificate requirements](apps.md#devices-with-their-own-certificate). Select refresh. |
| A device tile disappears | Check the [device discovery rules](apps.md#devices) and [preview visibility](apps.md#previews). A restart clears the list; devices must answer again. |
| A published preview or new card text is missing | Check `TAILHOME_PREVIEW_PORTS`, port access and the [card format](apps.md#preview-cards). Follow the [refresh steps](apps.md#card-refresh-and-checks), including the wait before retrying. |
| The ring is green but the preview app is broken | Check the app itself; the [ring only tests TLS](apps.md#using-the-page). To remove your preview, remove both [card and app mounts](apps.md#preview-cards). |
| tailhome exits mentioning `TAILHOME_PREVIEW_PORTS` | Fix the [port syntax](setup.md#preview-ports) and restart. |
| `home/` doesn't load | Without OAuth, use the logged address, such as `home-1`. With OAuth, define `svc:home` and add `autoApprovers` ([OAuth setup](setup.md#set-up-oauth)), or approve its host on the Services page. |
| `home/` doesn't resolve on one device | Enable MagicDNS. For Services, check the [required Tailscale version](../README.md#quick-start); on Linux before 1.94, run `sudo tailscale set --accept-routes`. |
| `https://home/` won't connect | HTTPS requires the full name. Open `http://home/` to redirect there. |
| `https://home.<tailnet>.ts.net/` won't connect | Enable HTTPS Certificates in the DNS settings. |
| The first HTTPS load is slow or times out | Tailscale may still be issuing the certificate. Retry in a minute. |
| tailhome exits with `this node joined without a tag` | Reset its state ([OAuth step 4](setup.md#set-up-oauth)) so it rejoins with its tag. |
| tailhome exits with `joining the tailnet: creating an auth key for tag:tailhome: …` | If the message says `API token invalid`, fix `TAILHOME_CLIENT_ID` and `TAILHOME_CLIENT_SECRET`. Otherwise give the OAuth client Auth Keys: Write for `tag:tailhome` and add the tag to `tagOwners`. |
| "Refresh failed: …" mentions `oauth2` or `403` | Give the OAuth client Services: Read. |
| A dot is red but the app opens | Allow tailhome to reach the app. With OAuth, add the grants from [step 1](setup.md#set-up-oauth). |
| A dot changes after a `url` override | For Services, HTTP(S) URLs check that address; other URLs with a port check a TCP connection there, otherwise the Service's port. Device checks keep using the original device address; see [checks by port](apps.md#card-refresh-and-checks). |
| `home/` stopped loading after months | Without credentials, the node's key probably expired. Restart tailhome, open the login link in its logs, and disable key expiry on the [Machines](https://console.tailscale.com/admin/machines) page. |
| An override has no effect | Check the logs for unmatched names, then check the [override path and file rules](customize.md#overrides). |
| "Refresh failed: … permission denied" | Fix the [override file and directory permissions](customize.md#overrides). |

## A permanent device app moved into Previews

Its `/.well-known/tailhome.json` card may contain `"preview": true`, including on port 443. To keep it under Apps and visible when offline, add this [override](customize.md#overrides), replacing `nas` with the actual device name, then refresh:

```yaml
nas:
  temporary: false
```

On port 443 this also restores HTTP checks at `/`. Remove `preview: true` from the card if you control it. Extra configured ports remain TLS-checked even with this override. A title/description-only card on 443 remains an ordinary app. There is no automatic preview expiry.

## The preview helper cannot find the Tailscale CLI

A shell alias is not enough. Set `TAILSCALE` to the executable path in the environment used to launch your agent. On macOS, the app normally includes `/Applications/Tailscale.app/Contents/MacOS/Tailscale`; the helper sets `TAILSCALE_BE_CLI=1` for its macOS subprocesses. Restart GUI-launched agents after changing their environment. See the [installation and Python prerequisites](../skills/tailnet-preview/references/serve.md#installation-and-updates). Do not install or switch Tailscale variants just for the skill.

## Serve returns a consent URL or permission error

Preserve the actual stderr for support. Complete administrator-approved Serve, MagicDNS, HTTPS certificate or policy setup yourself, then ask the agent to retry. A connected CLI alone does not prove Serve is ready. The skill must not use sudo, sign in, elevate privileges or change tailnet policy. See [direct preview setup](setup.md#publish-a-direct-preview).

## A viewer cannot open a host-verified preview URL

Host-side verification does not prove viewer connectivity or policy. Connect Tailscale on the review device and check its approved access to the publishing machine and HTTPS port. For someone outside your tailnet, arrange an administrator-approved device share or invitation. Keep the publishing computer awake, connected and running the app; Serve does not host or restart it. Check the app separately from the launcher's HTTPS ring. Do not claim a viewer test passed unless it was performed, or use a public fallback. See [preview privacy and lifetime](apps.md#previews).
