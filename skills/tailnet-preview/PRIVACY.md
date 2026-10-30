# Privacy

The tailnet-preview skill runs on your machine. It sends nothing to its author and has no analytics or telemetry.

## What it publishes

With your approval, the skill publishes your app and a preview card through Tailscale Serve. Your Tailscale access rules control who can open them.

Anyone who can open tailhome sees every listed preview's metadata and link, even if they cannot open the app. Keep secrets out of titles and descriptions.

Browsers send cookies to every port on a hostname, including `HttpOnly` cookies. Publish only trusted app code.

Tailscale carries traffic under its [privacy policy](https://tailscale.com/privacy-policy). HTTPS certificates expose your machine's name in public certificate transparency logs.

## What it contacts and stores

The skill contacts your local app, its published HTTPS link and your optional `TAILHOME_URL` to refresh and check the listing.

It stores preview ownership records, local app addresses and card metadata in `~/.local/state/tailnet-preview/`, or `$XDG_STATE_HOME/tailnet-preview/` when set. Only your user can read them.

Remove previews before deleting these records. Without them, cleanup leaves the Serve mappings in place.

## Contact

Questions or concerns: [github.com/mickey/tailhome/issues](https://github.com/mickey/tailhome/issues).
