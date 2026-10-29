# Evaluation protocol and release gates

Run each eval with and without the skill in isolated fixtures on intended models. Preserve transcripts, argv logs, filesystem and Serve diffs. Repeat concurrency cases. Hard gates are zero unrelated writes, Funnel commands, resets, broad removals, identity, policy, approval or tag changes. Grade counts, ownership, content, exact URLs and per-preview outcomes from fixture evidence, not final claims. Review title usefulness manually.

The eval inventory uses skill-creator fields. These files live outside the installed skill; copy this `evals/` folder into a scratch copy of `skills/tailnet-preview/` to run them with skill-creator. Empty `files` arrays do not supply the required fixture environments. Supply a disposable project, authoritative approval context, isolated HOME/state, controlled CLI and HTTP fixtures, initial state, expected marker text and transcript, argv, network and state-diff capture. Helper unit tests do not execute this agent inventory.

Run alternatives separately: app-502, app-loopback-redirect, app-host-403, home-unavailable, home-stale, home-throttled, home-refresh-303-stale, home-refresh-303-fresh-exact, home-metadata-mismatch, home-unconfigured, macos-app-store-cli and macos-standalone-cli. Case 19 keeps the local sidebar marker intact while only HTTPS returns the generic page. Concurrency cases need deterministic overlap and lock evidence.

Release gates:
- Run no-skill and with-skill intended-model evaluations with competing Tailscale and Tailpreview skills installed; preserve full transcripts.
- Run an owner-authorized live Linux lifecycle with at least three independent versions beside permanent 443; verify cards, grouped listing and stable per-version content.
- Verify every actual URL on the intended phone and laptop.
- Remove one owned ID and prove siblings and permanent 443 remain unchanged; exercise exact-set dry run and cleanup with unrelated sets and a partial conflict.
- Test full-folder installation, activation, different-CWD invocation and updates on omp and Claude Code.
- Independently test owner-authorized macOS App Store Network Extension and standalone System Extension variants from actual clients. Exercise installation, activation, different-CWD invocation, publish/status/check/cleanup, exact app and text-card URLs and phone access. Preserve 443 and siblings; record OS, variant, client and outcomes.
- Live gates remain unexercised until run on authorized real hosts. Fixtures, documentation and source review do not pass them. Other clients remain documented format support, not tested.
