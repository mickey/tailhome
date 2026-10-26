# Troubleshooting

[Back to the README](../README.md).

| Symptom | Cause and next step |
|---|---|
| The page lists no apps | Allow tailhome's node to reach your Services and devices. If Services stay missing, [set up OAuth](setup.md#set-up-oauth) to list them through the API. |
| A device app doesn't appear | Check the [device discovery rules](apps.md#devices) and allow tailhome to reach it on port 443. Select **refresh**; large tailnets may need several refreshes. For logged certificate errors, use a [certificate exception](apps.md#devices-with-their-own-certificate). |
| A device tile disappears | Check whether the device left the tailnet, was renamed, is hidden, no longer matches `TAILHOME_DEVICE_TAGS`, or shares a Service's name. After a tailhome restart, it must answer HTTPS again. |
| `home/` doesn't load | Without OAuth, use the logged address, such as `home-1`. With OAuth, define `svc:home` and add `autoApprovers` ([OAuth setup](setup.md#set-up-oauth)), or approve its host on the Services page. |
| `home/` doesn't resolve on one device | Enable MagicDNS. For Services, check the [required Tailscale version](../README.md#quick-start); on Linux before 1.94, run `sudo tailscale set --accept-routes`. |
| `https://home/` won't connect | HTTPS requires the full name. Open `http://home/` to redirect there. |
| `https://home.<tailnet>.ts.net/` won't connect | Enable HTTPS Certificates in the DNS settings. |
| The first HTTPS load is slow or times out | Tailscale may still be issuing the certificate. Retry in a minute. |
| tailhome exits with `this node joined without a tag` | Reset its state ([OAuth step 4](setup.md#set-up-oauth)) so it rejoins with its tag. |
| tailhome exits with `joining the tailnet: creating an auth key for tag:tailhome: …` | If the message says `API token invalid`, fix `TAILHOME_CLIENT_ID` and `TAILHOME_CLIENT_SECRET`. Otherwise give the OAuth client Auth Keys: Write for `tag:tailhome` and add the tag to `tagOwners`. |
| "Refresh failed: …" mentions `oauth2` or `403` | Give the OAuth client Services: Read. |
| A dot is red but the app opens | Allow tailhome to reach the app. With OAuth, add the grants from [step 1](setup.md#set-up-oauth). |
| A dot changes after a `url` override | For Services, HTTP(S) URLs check that address; other URLs with a port check a TCP connection there, otherwise the Service's port. Device checks still use their HTTPS root. Check that tailhome can reach the checked address. |
| `home/` stopped loading after months | Without credentials, the node's key probably expired. Restart tailhome, open the login link in its logs, and disable key expiry on the [Machines](https://console.tailscale.com/admin/machines) page. |
| An override has no effect | Check the logs for unmatched names, then check the [override path and file rules](customize.md#overrides). |
| "Refresh failed: … permission denied" | Fix the [override file and directory permissions](customize.md#overrides). |
