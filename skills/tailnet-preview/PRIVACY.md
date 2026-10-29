# Privacy

The tailnet-preview skill runs on your machine. It has no server, account, analytics or telemetry, and sends nothing to its author.

## What it publishes

When you approve a preview, the skill uses your Tailscale CLI to publish the app at `https://<this machine>.<your tailnet>.ts.net:<port>/`, together with a small JSON card at `/.well-known/tailhome.json` holding the title, description, optional icon name, set and version that the agent wrote. Any device that your Tailscale access rules allow can open both. Tailscale carries the traffic; its own [privacy policy](https://tailscale.com/privacy-policy) applies to the Tailscale service.

## What it contacts

- The app on `127.0.0.1`, to check it answers.
- The published link, through the local Tailscale CLI, to check it works.
- Only if you give it a tailhome address: that tailhome, to ask for a refresh and read its list. tailhome is a server you run yourself on your tailnet; the request contains no data beyond the refresh itself.

It contacts no other server.

## What it stores

A record of each preview it created (ID, machine, port, local app address, card, check settings and timestamps) in `~/.local/state/tailnet-preview/`, or under `$XDG_STATE_HOME`, readable only by your user. Cleanup uses it to remove only what the skill published. Delete the directory to forget all records.

## Contact

Questions or concerns: [github.com/mickey/tailhome/issues](https://github.com/mickey/tailhome/issues).
