self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.hypnoview;
  system = pkgs.stdenv.hostPlatform.system;
  selfPackages = if self == null then { } else self.packages or { };
  systemPackages = selfPackages.${system} or { };
in
{
  options.services.hypnoview = {
    enable = lib.mkEnableOption "hypnoview rolling popularity index and web UI";

    package = lib.mkOption {
      type = lib.types.package;
      default = systemPackages.hypnoview or pkgs.hypnoview;
      defaultText = lib.literalExpression "self.packages.\${pkgs.stdenv.hostPlatform.system}.default";
      description = "The hypnoview package to run.";
    };

    listenAddr = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8080";
      description = "HTTP listen address.";
    };

    databasePath = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/hypnoview/hypnoview.db";
      description = "Path to the SQLite database file.";
    };

    pollInterval = lib.mkOption {
      type = lib.types.str;
      default = "2h";
      description = "Interval between upstream Hypnohub poll sweeps.";
    };

    minPosts = lib.mkOption {
      type = lib.types.int;
      default = 10000;
      description = "Minimum number of posts to ensure are seeded in the database.";
    };

    enablePoller = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Whether to enable background polling and auto-seeding from upstream Hypnohub.";
    };

    extraArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Extra command-line arguments passed to hypnoview.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.hypnoview = {
      description = "hypnoview rolling popularity index and web UI";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      serviceConfig = {
        ExecStart = lib.escapeShellArgs (
          [
            (lib.getExe cfg.package)
            "--listen-addr"
            cfg.listenAddr
            "--db"
            (toString cfg.databasePath)
            "--poll-interval"
            cfg.pollInterval
            "--min-posts"
            (toString cfg.minPosts)
          ]
          ++ lib.optional (!cfg.enablePoller) "--no-poller"
          ++ cfg.extraArgs
        );

        DynamicUser = true;
        StateDirectory = "hypnoview";
        WorkingDirectory = "/var/lib/hypnoview";
        Restart = "on-failure";
        RestartSec = "5s";

        # Sandboxing / Hardening
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = lib.optional (
          !lib.hasPrefix "/var/lib/hypnoview" (toString cfg.databasePath)
        ) (builtins.dirOf (toString cfg.databasePath));
      };
    };
  };
}
