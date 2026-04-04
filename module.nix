self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.github-relay;

  consumerToJSON = name: consumer: {
    inherit name;
    inherit (consumer) repo events action;
  } // lib.optionalAttrs (consumer.branches != []) {
    inherit (consumer) branches;
  } // lib.optionalAttrs (consumer.action == "systemd") {
    inherit (consumer) unit;
  } // lib.optionalAttrs (consumer.action == "http") {
    inherit (consumer) url;
  } // lib.optionalAttrs (consumer.action == "command") {
    inherit (consumer) command;
  };

  configJSON = pkgs.writeText "github-relay-config.json" (builtins.toJSON {
    port = cfg.port;
    webhook_secret_file = cfg.webhookSecretFile;
    consumers = lib.mapAttrsToList consumerToJSON cfg.consumers;
  });

  relayPackage = self.packages.${pkgs.system}.default;
in
{
  options.services.github-relay = {
    enable = lib.mkEnableOption "GitHub webhook relay";

    port = lib.mkOption {
      type = lib.types.port;
      default = 8077;
      description = "Port the relay listens on.";
    };

    webhookSecretFile = lib.mkOption {
      type = lib.types.path;
      description = "Path to file containing the GitHub webhook secret.";
    };

    funnel = {
      enable = lib.mkEnableOption "Tailscale Funnel integration";
    };

    consumers = lib.mkOption {
      type = lib.types.attrsOf (lib.types.submodule {
        options = {
          repo = lib.mkOption {
            type = lib.types.str;
            description = "Repository full name (owner/repo) or '*' for all.";
          };

          events = lib.mkOption {
            type = lib.types.listOf lib.types.str;
            description = "List of GitHub event types to match.";
          };

          branches = lib.mkOption {
            type = lib.types.listOf lib.types.str;
            default = [];
            description = "Branch filter. Empty means all branches.";
          };

          action = lib.mkOption {
            type = lib.types.enum [ "systemd" "http" "command" ];
            description = "Action type for this consumer.";
          };

          unit = lib.mkOption {
            type = lib.types.str;
            default = "";
            description = "Systemd unit to start (for action = 'systemd').";
          };

          url = lib.mkOption {
            type = lib.types.str;
            default = "";
            description = "URL to POST to (for action = 'http').";
          };

          command = lib.mkOption {
            type = lib.types.str;
            default = "";
            description = "Shell command to run (for action = 'command').";
          };
        };
      });
      default = {};
      description = "Webhook consumers.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.etc."github-relay/config.json".source = configJSON;

    systemd.services.github-relay = {
      description = "GitHub Webhook Relay";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" "tailscaled.service" ];

      serviceConfig = {
        ExecStart = "${relayPackage}/bin/github-relay -config /etc/github-relay/config.json";
        Restart = "on-failure";
        RestartSec = 5;

        DynamicUser = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        NoNewPrivileges = true;
        ReadOnlyPaths = [ "/" ];
        ReadWritePaths = [ ];

        # Allow reading the webhook secret
        SupplementaryGroups = [ ];
      };
    };

    services.tailscale.permitCertUid = lib.mkIf cfg.funnel.enable "github-relay";

    services.tsnsrv.services.github-relay = lib.mkIf cfg.funnel.enable {
      listenAddress = ":443";
      ephemeral = false;
      funnel = true;
      toURL = "http://127.0.0.1:${toString cfg.port}/hooks/github";
    };
  };
}
