self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfgGaze = config.services.hypnogaze;
  cfgView = config.services.hypnoview;
  cfg = if cfgGaze.enable then cfgGaze else cfgView;
  isGaze = cfgGaze.enable || !cfgView.enable;

  system = pkgs.stdenv.hostPlatform.system;
  selfPackages = if self == null then { } else self.packages or { };
  systemPackages = selfPackages.${system} or { };

  mkOptions = serviceName: defaultDb: {
    enable = lib.mkEnableOption "${serviceName} rolling popularity index and web UI";

    package = lib.mkOption {
      type = lib.types.package;
      default = systemPackages.hypnogaze or systemPackages.hypnoview or pkgs.hypnogaze or pkgs.hypnoview;
      defaultText = lib.literalExpression "self.packages.\${pkgs.stdenv.hostPlatform.system}.default";
      description = "The ${serviceName} package to run.";
    };

    listenAddr = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8080";
      description = "HTTP listen address.";
    };

    databasePath = lib.mkOption {
      type = lib.types.path;
      default = defaultDb;
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
      description = "Extra command-line arguments passed to ${serviceName}.";
    };
  };
in
{
  options.services.hypnogaze = mkOptions "hypnogaze" "/var/lib/hypnogaze/hypnogaze.db";
  options.services.hypnoview = mkOptions "hypnoview" "/var/lib/hypnoview/hypnoview.db";

  config = lib.mkIf (cfgGaze.enable || cfgView.enable) {
    systemd.services.${if isGaze then "hypnogaze" else "hypnoview"} = {
      description = "${if isGaze then "hypnogaze" else "hypnoview"} rolling popularity index and web UI";
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
        StateDirectory = if isGaze then "hypnogaze" else "hypnoview";
        WorkingDirectory = if isGaze then "/var/lib/hypnogaze" else "/var/lib/hypnoview";
        Restart = "on-failure";
        RestartSec = "5s";

        # Sandboxing / Hardening
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = lib.optional (
          !lib.hasPrefix (if isGaze then "/var/lib/hypnogaze/" else "/var/lib/hypnoview/") (toString cfg.databasePath)
        ) (builtins.dirOf (toString cfg.databasePath));
      };
    };
  };
}
