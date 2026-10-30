# Troubleshooting

[Back to the README](../README.md).

| Symptom | Fix |
|---|---|
| No apps listed | Allow tailhome to reach Services and devices; [configure OAuth](setup.md#set-up-oauth) if Services remain missing. |
| Device app missing | Check [discovery](apps.md#devices), port access and [certificates](apps.md#devices-with-their-own-certificate), then refresh. |
| Preview or card text missing | Check `TAILHOME_PREVIEW_PORTS`, port access and [card format](apps.md#preview-cards), then refresh. |
| Preview disappears or won't open | Check [viewer access and host availability](apps.md#previews); keep the host and app running. |
| Green ring, broken app | A ring checks HTTPS connectivity, not the page; open the app to test it. |
| Invalid `TAILHOME_PREVIEW_PORTS` | Fix the [port syntax](setup.md#preview-ports), then run `docker compose up -d` (or restart the binary). |
| `home/` won't load | Use the logged address without OAuth; with OAuth, define `svc:home` and approve its host ([setup](setup.md#set-up-oauth)). |
| `home/` won't resolve | Check [MagicDNS](https://tailscale.com/docs/features/magicdns) and [client requirements](setup.md#before-you-start). |
| `https://home/` won't connect | Use `http://home/`; for the full `.ts.net` address, check [HTTPS setup](https://tailscale.com/docs/how-to/set-up-https-certificates). |
| First HTTPS request times out | Retry in a minute while Tailscale issues the certificate. |
| `this node joined without a tag` | Reset tailhome's state using [OAuth setup](setup.md#set-up-oauth). |
| `can't create its own auth key` | Open the logged login link as an admin, or grant Auth Keys: Write for `tag:tailhome`. |
| Login link does nothing | Approve tailhome on [Machines](https://console.tailscale.com/admin/machines). |
| Login link keeps appearing | Restart and open the new link; if it persists, [report it](https://github.com/mickey/tailhome/issues), removing every login link from shared logs. |
| Auth-key creation returns `401` | Fix `TAILHOME_CLIENT_ID` and `TAILHOME_CLIENT_SECRET`. |
| Auth key is `invalid or not permitted` | Grant Auth Keys: Write for `tag:tailhome` and add it to `tagOwners` ([OAuth setup](setup.md#set-up-oauth)). |
| Refresh reports OAuth `401` or `403` | Fix OAuth credentials for `401`; grant Services: Read for `403`. |
| Red dot, working app | Allow tailhome to reach the app through your [access rules](setup.md#set-up-oauth). |
| `home/` stops after months | Restart and open the new login link; see [key expiry](https://tailscale.com/docs/features/access-control/key-expiry). |
| Override ignored or permission denied | Check names, path and [file permissions](customize.md#overrides). |
| Serve returns consent or permission errors | Follow [preview setup](setup.md#publish-a-direct-preview); the skill must not sign in, use sudo or change tailnet policy. |

## A permanent device app moved into previews

Set `nas: {temporary: false}` in your [overrides](customize.md#overrides), replacing `nas` with the device name, then refresh.

## The preview helper cannot find the Tailscale CLI

Set `TAILSCALE` to the executable path in your agent's launch environment, then restart the agent; see [CLI discovery](../skills/tailnet-preview/references/serve.md#find-the-tailscale-cli).
