# tailnet-preview

Ask your coding agent "can I see this on my phone?" and get a private HTTPS link. This skill lets an agent publish the app it is working on with [Tailscale Serve](https://tailscale.com/kb/1312/serve), check that the link works, hand it to you, and later remove only what it published. Several versions of an app become several previews, grouped as one set. If you run [tailhome](https://github.com/mickey/tailhome), each preview also appears at the top of your start page.

## Requirements

- Linux or macOS, on the machine where the app runs. Native Windows is not supported.
- Python 3.9 or later. The helper script uses only the standard library.
- The Tailscale CLI, logged in, with MagicDNS and HTTPS certificates enabled for the tailnet.
- Permission to change Tailscale Serve. On Linux, the machine's owner runs `sudo tailscale set --operator=$USER` once. The skill never uses sudo itself.

## What it changes on your machine

- Before publishing, the agent asks which HTTPS ports it may use, suggesting 8443 to 8452, unless you already approved a range.
- For each preview, it adds two background Tailscale Serve mappings on one free approved port: the app at `/`, and a small JSON card at `/.well-known/tailhome.json` with the title, description and optional icon name the agent chose.
- It records what it created in a private state directory, `~/.local/state/tailnet-preview/` (or `$XDG_STATE_HOME`), so cleanup removes only those mappings.
- It never uses Funnel, `tailscale serve reset`, whole-port removal, or changes to tailnet settings, tags or access rules. It does not stop or start apps without your permission.

## What it sends, and to whom

- Nothing goes to the skill's author, and there is no analytics or backend.
- The preview itself is network publication on purpose: devices that your Tailscale access rules allow can open the app and read its card over your tailnet. Treat the title and description as visible to them.
- The helper checks the app on `127.0.0.1` and the published link through the local Tailscale CLI (`tailscale nc`), and runs read-only `tailscale status` and `tailscale serve status` commands.
- If you give it a tailhome address, it asks that tailhome to refresh and reads its list to confirm the preview is listed. It contacts no other server.

## Use

Ask in plain words, for example "publish this so I can open it on my phone", "show me three versions of the hero", or "remove the landing previews". The agent follows [SKILL.md](SKILL.md) and runs `scripts/tailnet_preview.py` (`preflight`, `publish`, `status`, `list`, `cleanup`).

More detail: [references/serve.md](references/serve.md). Source, issues and license (MIT): [mickey/tailhome](https://github.com/mickey/tailhome).

Install in Claude Code with `/plugin marketplace add mickey/tailhome`, then `/plugin install tailnet-preview@tailhome`.
