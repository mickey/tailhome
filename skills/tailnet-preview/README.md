# tailnet-preview

tailnet-preview is an agent skill for developers who use Tailscale. Ask your coding agent "can I see this on my phone?" and get a private HTTPS link. The agent publishes the app it is working on with [Tailscale Serve](https://tailscale.com/kb/1312/serve), checks that the link works, hands it to you, and later removes only what it published. Several versions of an app become several previews, grouped as one set. If you run [tailhome](https://github.com/mickey/tailhome), the self-hosted start page for your tailnet, each preview also appears at the top of it.

## Requirements

- Linux or macOS, on the machine where the app runs. Native Windows is not supported.
- Python 3.9 or later. The helper script uses only the standard library.
- The Tailscale CLI, logged in, with MagicDNS and HTTPS certificates enabled for the tailnet.
- Permission to change Tailscale Serve. On Linux, the machine's owner runs `sudo tailscale set --operator=$USER` once. The skill never uses sudo itself.

## What it changes on your machine

- Before publishing, the agent asks which HTTPS ports it may use, suggesting 8443 to 8452, unless you already approved a range.
- For each preview, it adds two background Tailscale Serve mappings on one free approved port. The app is served at `/`. A small JSON card at `/.well-known/tailhome.json` holds the title, description, optional icon name, set and version the agent chose, plus `"preview": true`.
- It records what it created in a private state directory, `~/.local/state/tailnet-preview/` (or `$XDG_STATE_HOME/tailnet-preview/` when that variable is set), so cleanup removes only those mappings.
- It never uses Funnel, `tailscale serve reset`, whole-port removal, or changes to tailnet settings, tags or access rules. It does not stop or start apps without your permission.

## What it runs and sends

- Nothing goes to the skill's author, and there is no analytics or backend.
- Publishing is the point: any device that your Tailscale access rules allow can open the app and read its card over your tailnet. Treat the card's contents as visible to them.
- The helper checks the app on loopback (`127.0.0.1` or `::1`) and opens the published link through `tailscale nc`.
- It runs these Tailscale commands: read-only `tailscale version`, `tailscale status --json`, `tailscale serve status --json` and, on Linux, `tailscale debug prefs` to read the operator setting; `tailscale serve --bg ...` to add a mapping and `tailscale serve ... off` to remove it. The CLI runs with the agent's environment, plus `TAILSCALE_BE_CLI=1` on macOS so the app's executable acts as the CLI. Nothing from the environment is sent anywhere else.
- The helper reads only these environment variables: `TAILSCALE` (CLI path), `TAILNET_PREVIEW_PORTS`, `TAILHOME_URL` and `XDG_STATE_HOME`.
- If you give it a tailhome address, it sends `POST /refresh` with an empty body and reads `/api/services.json` and `/api/list.json` to confirm the preview is listed. Anyone who can open that tailhome then sees the card and link, even if access rules block them from the app. It contacts no other server.

## Use

Ask in plain words, for example "publish this so I can open it on my phone", "show me three versions of the hero", or "remove the landing previews". The agent follows [SKILL.md](SKILL.md) and runs `scripts/tailnet_preview.py` (`preflight`, `publish`, `status`, `list`, `cleanup`).

More detail: [references/serve.md](references/serve.md). Privacy: [PRIVACY.md](PRIVACY.md). Source, issues and license (MIT): [mickey/tailhome](https://github.com/mickey/tailhome).

To install from GitHub instead of the directory, run `/plugin marketplace add mickey/tailhome`, then `/plugin install tailnet-preview@tailhome`.
