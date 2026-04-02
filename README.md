# github-relay

A NixOS module that receives GitHub webhooks via [Tailscale Funnel](https://tailscale.com/kb/1223/funnel) and dispatches them to local consumers. One webhook endpoint, multiple subscribers — no port forwarding, no DNS, automatic HTTPS.

## Why

Home servers behind NAT can't easily receive GitHub webhooks. Tailscale Funnel solves the ingress problem, but you still need something to route events to the right place. github-relay is that router.

Example use cases:
- **NixOS auto-rebuild** when your config repo is pushed
- **[klaus](https://github.com/patflynn/klaus) push notifications** — react to CI completions and PR reviews in real-time instead of polling
- **Deploy notifications** — trigger service restarts, cache warming, etc.

## Architecture

```
GitHub ──webhook──▶ Tailscale Funnel ──▶ github-relay ──▶ consumers
                    (HTTPS, public)      (validate sig,    ├─ systemd unit
                                          match rules,     ├─ HTTP endpoint
                                          dispatch)        └─ script/command
```

The relay is intentionally simple: validate the webhook signature, match the event against consumer rules, dispatch. No queuing, no state, no database. Consumers handle their own idempotency.

## NixOS module usage

```nix
# flake.nix
{
  inputs.github-relay.url = "github:patflynn/github-relay";

  outputs = { self, nixpkgs, github-relay, ... }: {
    nixosConfigurations.classic-laddie = nixpkgs.lib.nixosSystem {
      modules = [
        github-relay.nixosModules.default
        ./configuration.nix
      ];
    };
  };
}
```

```nix
# configuration.nix
{
  services.github-relay = {
    enable = true;

    # Port the relay listens on (Funnel forwards here)
    port = 8077;

    # GitHub webhook secret (use agenix/sops-nix in practice)
    webhookSecretFile = config.age.secrets.github-webhook.path;

    # Expose via Tailscale Funnel
    funnel = {
      enable = true;
      # Results in: https://<hostname>.<tailnet>.ts.net/hooks/github
    };

    consumers = {
      # Auto-rebuild NixOS when cosmo is pushed
      cosmo-rebuild = {
        repo = "patflynn/cosmo";
        events = [ "push" ];
        branches = [ "main" ];
        action = "systemd";
        unit = "cosmo-rebuild";
      };

      # Feed events to klaus for real-time pipeline updates
      klaus = {
        repo = "*";
        events = [ "push" "check_run" "check_suite" "pull_request" "pull_request_review" ];
        action = "http";
        url = "http://localhost:9800/webhook/github";
      };

      # Run an arbitrary script
      deploy-notify = {
        repo = "patflynn/myapp";
        events = [ "push" ];
        branches = [ "main" ];
        action = "command";
        command = "/run/current-system/sw/bin/notify-send 'myapp deployed'";
      };
    };
  };

  # The cosmo rebuild oneshot (triggered by the relay)
  systemd.services.cosmo-rebuild = {
    description = "Rebuild NixOS from cosmo";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = toString (pkgs.writeShellScript "cosmo-rebuild" ''
        cd /etc/cosmo
        ${pkgs.git}/bin/git pull origin main
        ${pkgs.nixos-rebuild}/bin/nixos-rebuild switch --flake .
      '');
    };
  };
}
```

## Consumer actions

| Action | Description |
|--------|-------------|
| `systemd` | Starts a systemd unit (oneshot). The webhook payload is passed via `GITHUB_EVENT` env var. |
| `http` | POSTs the raw webhook payload to a URL. Includes `X-GitHub-Event` and `X-GitHub-Delivery` headers. |
| `command` | Runs a shell command. Payload available on stdin and as `GITHUB_EVENT` env var. |

## Consumer matching

Each consumer specifies what events it cares about:

| Field | Description | Default |
|-------|-------------|---------|
| `repo` | Repository full name (`owner/repo`) or `"*"` for all | required |
| `events` | List of GitHub event types | required |
| `branches` | Branch filter (for push/PR events). Empty = all branches. | `[]` |

Events that don't match any consumer are silently dropped (200 OK to GitHub).

## Webhook setup

1. Enable Tailscale Funnel on your tailnet (Tailscale admin console)
2. Deploy the module — it configures Funnel automatically
3. In your GitHub repo (or org) settings, add a webhook:
   - **URL**: `https://<hostname>.<tailnet>.ts.net/hooks/github`
   - **Content type**: `application/json`
   - **Secret**: same as `webhookSecretFile`
   - **Events**: select the events your consumers need (or "Send me everything")

For org-wide hooks, configure once and all repos are covered.

## Security

- **Webhook signature validation**: Every request is verified against the shared secret using HMAC-SHA256. Invalid signatures are rejected with 403.
- **Tailscale Funnel**: Traffic is encrypted end-to-end. The relay only accepts traffic from Funnel (localhost or Tailscale interface).
- **No secrets in config**: Use `webhookSecretFile` with agenix or sops-nix. Never put secrets in your Nix config.
- **Consumer isolation**: Each consumer action runs with minimal privileges. Systemd units inherit their own service config. HTTP consumers only receive events they subscribed to.

## Implementation

```
github-relay/
├── cmd/relay/
│   └── main.go          # HTTP server, signature validation, dispatcher
├── internal/
│   ├── config/          # consumer rule matching
│   ├── dispatch/        # systemd, http, command dispatchers
│   └── webhook/         # GitHub signature validation
├── module.nix           # NixOS module
├── flake.nix            # Nix build + module export
└── README.md
```

The relay is a single Go binary (~500 lines). It reads a JSON config generated by the NixOS module, starts an HTTP server, and dispatches matched events.

## Reliability

- **Machine offline**: GitHub retries failed webhook deliveries for up to 3 days. Short outages (reboots, updates) are handled automatically.
- **Consumer failure**: If a systemd unit or HTTP call fails, the relay logs the error but returns 200 to GitHub (to avoid infinite retries for consumer-side issues). Consumer services should handle their own retry logic.
- **Startup ordering**: The module sets `after = [ "tailscaled.service" ]` to ensure Tailscale is ready before the relay starts.

## Status

Early design phase. Contributions and feedback welcome.

## License

MIT
