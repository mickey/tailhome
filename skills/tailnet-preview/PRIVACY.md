# Privacy

The tailnet-preview skill runs on your machine. It has no server, account, analytics or telemetry, and sends nothing to its author.

## What it publishes

When you approve a preview, the skill uses your Tailscale CLI to publish the app at `https://<this machine>.<your tailnet>.ts.net:<port>/`. A small JSON card at `/.well-known/tailhome.json` goes with it. The card holds the title, description, optional icon name, set and version that the agent wrote, plus `"preview": true`. Any device that your Tailscale access rules allow can open both.

Tailscale carries the traffic, and its own [privacy policy](https://tailscale.com/privacy-policy) applies to the Tailscale service. The first HTTPS request makes Tailscale obtain a certificate for this machine's name. As the [Tailscale HTTPS guide](https://tailscale.com/kb/1153/enabling-https) states, that name then appears in public certificate transparency logs.

## What it contacts

- The app on loopback (`127.0.0.1` or `::1`), to check it answers.
- The published link, through `tailscale nc`, to check it works.
- Only if you give it a tailhome address: that tailhome. The skill sends `POST /refresh` with an empty body and reads `/api/services.json` and `/api/list.json`. tailhome is a server you run yourself on your tailnet. Anyone who can open it then sees the preview's card and link, even if access rules block them from the app.

It contacts no other server.

## What it stores

A record of each preview it created (ID, machine, port, local app address, card, check settings and timestamps) in `~/.local/state/tailnet-preview/`, or `$XDG_STATE_HOME/tailnet-preview/` when that variable is set. Only your user can read it. Cleanup uses it to remove only what the skill published. Remove your previews before deleting the directory: without the records, the skill will not remove the remaining mappings.

## Contact

Questions or concerns: [github.com/mickey/tailhome/issues](https://github.com/mickey/tailhome/issues).
