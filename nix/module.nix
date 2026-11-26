# NixOS module for remote-agent. Imported by the flake as
# nixosModules.default = import ./nix/module.nix self;
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.remote-agent;
  inherit (lib)
    mkEnableOption
    mkIf
    mkMerge
    mkOption
    mkPackageOption
    optionalAttrs
    types
    ;

  stateDir = "/var/lib/remote-agent";
  credDir = "/run/credentials/remote-agent.service";
  appURL = "http://127.0.0.1:${toString cfg.port}";

  sandboxEnv = pkgs.buildEnv {
    name = "remote-agent-sandbox-env";
    paths = cfg.extraSandboxPackages;
    pathsToLink = [
      "/bin"
      "/share"
      "/etc"
      "/lib"
    ];
    ignoreCollisions = true;
  };

  # The Config contract of docs/architecture.md. Secrets are never inlined:
  # the app reads them from the files named here (systemd credentials).
  appConfig = {
    listen = "127.0.0.1:${toString cfg.port}";
    mcpListen = "127.0.0.1:${toString cfg.mcpPort}";
    previewListen = "127.0.0.1:${toString cfg.previewPort}";
    dataDir = stateDir;
    inherit (cfg)
      maxParallelRuns
      runTimeoutMinutes
      sandboxPaths
      browserMcpServers
      ;
    githubTokenFile = "${credDir}/github-token";
    claudeBin = lib.getExe cfg.claudePackage;
    piBin = lib.getExe cfg.piPackage;
    bwrapBin = lib.getExe cfg.bubblewrapPackage;
    # Bound read-only (it lives in /nix/store); its bin/ is the sandbox PATH.
    sandboxEnv = "${sandboxEnv}";
    mcpServers = lib.mapAttrs (_: s: {
      inherit (s) command args env;
    }) cfg.mcpServers;
    skillsSeedDir = "${cfg.package}/share/remote-agent/skills";
  }
  // optionalAttrs (cfg.openrouterKeyFile != null) {
    openrouterKeyFile = "${credDir}/openrouter-key";
  }
  // optionalAttrs (cfg.publicURL != null) { inherit (cfg) publicURL; }
  // optionalAttrs (cfg.previewURL != null) { inherit (cfg) previewURL; }
  // cfg.extraSettings;

  configFile = pkgs.writeText "remote-agent-config.json" (builtins.toJSON appConfig);

  autoDeployScript = pkgs.writeShellApplication {
    name = "remote-agent-auto-deploy";
    runtimeInputs = [
      pkgs.curl
      pkgs.jq
      pkgs.coreutils
      cfg.package
    ];
    text = builtins.readFile ./auto-deploy.sh;
  };

  tailscaleServeScript = pkgs.writeShellApplication {
    name = "remote-agent-tailscale-serve";
    runtimeInputs = [
      config.services.tailscale.package
      pkgs.jq
      pkgs.coreutils
    ];
    text = ''
      # Wait until this node is logged in to the tailnet (`tailscale up`).
      until [ "$(tailscale status --json --peers=false | jq -r .BackendState)" = Running ]; do
        echo "waiting for tailscale to be up (run: tailscale up)"
        sleep 10
      done
      # Both commands replace any existing handler on the same port, so
      # running them again is harmless.
      tailscale serve --bg --https=${toString cfg.tailscale.httpsPort} ${appURL}
      tailscale serve --bg --https=${toString cfg.tailscale.previewHttpsPort} http://127.0.0.1:${toString cfg.previewPort}
      tailscale serve status
    '';
  };

  mcpServerType = types.submodule {
    options = {
      command = mkOption {
        type = types.str;
        description = "Executable of the MCP server (stdio transport).";
      };
      args = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Arguments passed to the MCP server.";
      };
      env = mkOption {
        type = types.attrsOf types.str;
        default = { };
        description = "Environment variables for the MCP server.";
      };
    };
  };

  portOption =
    default: description:
    mkOption {
      type = types.port;
      inherit default description;
    };
in
{
  options.services.remote-agent = {
    enable = mkEnableOption "remote-agent, a self-hosted board where coding agents work tickets";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.remote-agent;
      defaultText = lib.literalExpression "remote-agent.packages.\${system}.remote-agent";
      description = "The remote-agent package.";
    };

    port = portOption 7700 "Port of the web app and API. Always bound to 127.0.0.1.";
    mcpPort = portOption 7701 "Port of the app's MCP server. Always bound to 127.0.0.1.";
    previewPort = portOption 7702 "Port of the prototype preview proxy. Always bound to 127.0.0.1.";

    maxParallelRuns = mkOption {
      type = types.ints.positive;
      default = 2;
      description = "Maximum number of agent runs at once.";
    };

    runTimeoutMinutes = mkOption {
      type = types.ints.positive;
      default = 120;
      description = "Minutes an agent run may take before it is stopped. A project can set its own limit in the app.";
    };

    githubTokenFile = mkOption {
      type = types.path;
      example = "/var/lib/secrets/remote-agent/github-token";
      description = ''
        File with a fine-grained GitHub PAT. Read by systemd as a credential,
        so it may be owned by root with mode 0400. The service does not start
        while the file is missing.
      '';
    };

    openrouterKeyFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/var/lib/secrets/remote-agent/openrouter-key";
      description = "File with the OpenRouter API key used by Pi runs (systemd credential).";
    };

    publicURL = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "https://agent.tailnet-name.ts.net";
      description = "URL the app is reached at over Tailscale (used in links and Web Push).";
    };

    previewURL = mkOption {
      type = types.nullOr types.str;
      default = null;
      example = "https://agent.tailnet-name.ts.net:8443";
      description = "URL of the prototype preview over Tailscale.";
    };

    claudePackage = mkPackageOption pkgs "claude-code" { };
    piPackage = mkPackageOption pkgs "pi-coding-agent" { };
    bubblewrapPackage = mkPackageOption pkgs "bubblewrap" { };

    mcpServers = mkOption {
      type = types.attrsOf mcpServerType;
      default = {
        playwright = {
          # The nixpkgs wrapper pins PLAYWRIGHT_BROWSERS_PATH to
          # playwright-driver.browsers and defaults to chromium.
          command = lib.getExe pkgs.playwright-mcp;
          args = [
            "--headless"
            "--browser"
            "chromium"
          ];
        };
      };
      defaultText = lib.literalExpression ''
        {
          playwright = {
            command = lib.getExe pkgs.playwright-mcp;
            args = [ "--headless" "--browser" "chromium" ];
          };
        }
      '';
      description = "MCP servers given to every agent run (rendered to Claude --mcp-config and Pi mcp.json).";
    };

    browserMcpServers = mkOption {
      type = types.listOf types.str;
      default = lib.filter (n: n == "playwright") (lib.attrNames cfg.mcpServers);
      defaultText = lib.literalExpression ''[ "playwright" ]'';
      description = "MCP servers that drive a browser; only one run at a time may use them.";
    };

    extraSandboxPackages = mkOption {
      type = types.listOf types.package;
      default = with pkgs; [
        bashInteractive
        coreutils
        findutils
        diffutils
        gnugrep
        gnused
        gawk
        gnutar
        gzip
        xz
        unzip
        which
        file
        less
        procps
        git
        openssh
        curl
        jq
        cacert
        nodejs
        gnumake
        gcc
        ripgrep
        fd
        python3
        go
      ];
      defaultText = lib.literalMD "git, coreutils, bash, nodejs, gnumake, gcc, ripgrep, python3, go and common CLI tools";
      description = ''
        Packages on the sandbox PATH. They are joined into one buildEnv in
        /nix/store, which the sandbox binds read-only.
      '';
    };

    sandboxPaths = mkOption {
      type = types.listOf types.str;
      default = [ "/nix/store" ];
      description = "Host paths bound read-only into every sandbox.";
    };

    memoryHigh = mkOption {
      type = types.str;
      default = "1536M";
      description = ''
        MemoryHigh of remote-agent.slice, which holds the app and all its agent
        runs. Above it the kernel throttles and reclaims (zram) instead of
        OOM-killing. The app process itself has no MemoryMax.
      '';
    };

    extraSettings = mkOption {
      type = types.attrsOf types.anything;
      default = { };
      description = "Extra keys merged into config.json (overrides generated keys).";
    };

    tailscale = {
      enable = mkOption {
        type = types.bool;
        default = true;
        description = "Enable Tailscale and open only its UDP port in the firewall.";
      };
      authKeyFile = mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Auth key file for unattended `tailscale up` (services.tailscale.authKeyFile).";
      };
      serve = mkOption {
        type = types.bool;
        default = true;
        description = "Publish the app and the preview port to the tailnet with `tailscale serve` (disable in tests).";
      };
      httpsPort = portOption 443 "Tailnet HTTPS port for the app.";
      previewHttpsPort = portOption 8443 "Tailnet HTTPS port for prototype previews.";
    };

    autoDeploy = {
      enable = mkEnableOption "automatic deployment of new flake revisions between agent runs";
      flake = mkOption {
        type = types.str;
        example = "github:s1n7ax/remote-agent#droplet";
        description = "Flake reference with the nixosConfigurations attribute to deploy.";
      };
      interval = mkOption {
        type = types.str;
        default = "10min";
        description = "Time between checks (systemd time span).";
      };
      maxWaitHours = mkOption {
        type = types.ints.positive;
        default = 6;
        description = "How long to wait for running agents to finish before giving up (and undraining) until the next check.";
      };
      healthRetries = mkOption {
        type = types.ints.positive;
        default = 20;
        description = "Health check attempts (6 s apart) after switching before rolling back.";
      };
      package = mkOption {
        type = types.package;
        readOnly = true;
        default = autoDeployScript;
        description = "The generated auto-deploy script (exposed for tests).";
      };
    };
  };

  config = mkIf cfg.enable (mkMerge [
    {
      users.users.remote-agent = {
        isSystemUser = true;
        group = "remote-agent";
        home = stateDir;
        description = "remote-agent service";
      };
      users.groups.remote-agent = { };

      environment.systemPackages = [ cfg.package ];

      systemd.slices.remote-agent = {
        description = "remote-agent app and its agent runs";
        sliceConfig.MemoryHigh = cfg.memoryHigh;
      };

      systemd.services.remote-agent = {
        description = "remote-agent";
        wantedBy = [ "multi-user.target" ];
        after = [ "network-online.target" ];
        wants = [ "network-online.target" ];
        path = [
          pkgs.git
          cfg.bubblewrapPackage
        ];
        environment = {
          HOME = stateDir;
          SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
        };
        serviceConfig = {
          Slice = "remote-agent.slice";
          User = "remote-agent";
          Group = "remote-agent";
          DynamicUser = false;
          StateDirectory = "remote-agent";
          StateDirectoryMode = "0750";
          WorkingDirectory = stateDir;
          LoadCredential = [
            "github-token:${cfg.githubTokenFile}"
          ]
          ++ lib.optional (cfg.openrouterKeyFile != null) "openrouter-key:${cfg.openrouterKeyFile}";
          ExecStartPre = "${pkgs.coreutils}/bin/install -m 0640 ${configFile} ${stateDir}/config.json";
          ExecStart = "${lib.getExe cfg.package} serve --config ${stateDir}/config.json";
          Restart = "on-failure";
          RestartSec = 5;
          # An agent being OOM-killed must not take the whole app down.
          OOMPolicy = "continue";

          # Hardening, compatible with unprivileged bubblewrap (verified by
          # the VM test's bwrap-probe): bwrap needs user, mount, pid, net, ipc
          # and uts namespaces and mount syscalls, so RestrictNamespaces,
          # PrivateUsers and a SystemCallFilter are deliberately not set.
          # ProtectKernelTunables, ProtectKernelLogs, ProtectHostname and
          # ProtectProc are not set either: their overmounts on /proc make the
          # kernel refuse bwrap's fresh procfs ("VFS: Mount too revealing").
          # NoNewPrivileges is fine (bwrap is not setuid).
          NoNewPrivileges = true;
          ProtectSystem = "strict";
          ProtectHome = true;
          PrivateTmp = true;
          PrivateDevices = false; # bwrap --dev needs /dev/null etc.
          ProtectKernelModules = true;
          ProtectControlGroups = true;
          ProtectClock = true;
          RestrictRealtime = true;
          RestrictSUIDSGID = true;
          LockPersonality = true;
          RestrictAddressFamilies = [
            "AF_UNIX"
            "AF_INET"
            "AF_INET6"
            "AF_NETLINK"
          ];
          CapabilityBoundingSet = "";
          AmbientCapabilities = "";
          UMask = "0027";
        };
      };
    }

    (mkIf cfg.tailscale.enable {
      services.tailscale = {
        enable = true;
        openFirewall = true; # UDP services.tailscale.port only
        authKeyFile = cfg.tailscale.authKeyFile;
      };
      # Nothing else is opened: the app binds 127.0.0.1 and is reached only
      # through `tailscale serve`, so no interface needs to be trusted.
      networking.firewall.enable = true;
    })

    (mkIf (cfg.tailscale.enable && cfg.tailscale.serve) {
      systemd.services.remote-agent-tailscale-serve = {
        description = "Publish remote-agent to the tailnet (tailscale serve)";
        wantedBy = [ "multi-user.target" ];
        after = [
          "tailscaled.service"
          "tailscaled-autoconnect.service"
        ];
        wants = [ "tailscaled.service" ];
        unitConfig.StartLimitIntervalSec = 0;
        serviceConfig = {
          Type = "oneshot";
          RemainAfterExit = true;
          ExecStart = lib.getExe tailscaleServeScript;
          Restart = "on-failure";
          RestartSec = 30;
          TimeoutStartSec = "infinity";
        };
      };
    })

    (mkIf cfg.autoDeploy.enable {
      nix.settings.experimental-features = [
        "nix-command"
        "flakes"
      ];

      systemd.services.remote-agent-auto-deploy = {
        description = "Deploy the latest remote-agent flake revision between agent runs";
        after = [
          "network-online.target"
          "remote-agent.service"
        ];
        wants = [ "network-online.target" ];
        # Never restart or stop this unit from the switch it performs.
        restartIfChanged = false;
        unitConfig.X-StopOnRemoval = false;
        path = [
          config.nix.package
          config.system.build.nixos-rebuild
          pkgs.git
          pkgs.coreutils
        ];
        environment = {
          RA_FLAKE = cfg.autoDeploy.flake;
          RA_URL = appURL;
          RA_MAX_WAIT_SECS = toString (cfg.autoDeploy.maxWaitHours * 3600);
          RA_HEALTH_RETRIES = toString cfg.autoDeploy.healthRetries;
          RA_STATE_DIR = "/var/lib/remote-agent-deploy";
          # The running system's nixos-version knows its configurationRevision.
          RA_NIXOS_VERSION = "/run/current-system/sw/bin/nixos-version";
          HOME = "/root";
        };
        serviceConfig = {
          Type = "oneshot";
          ExecStart = lib.getExe autoDeployScript;
          StateDirectory = "remote-agent-deploy";
        };
      };

      systemd.timers.remote-agent-auto-deploy = {
        wantedBy = [ "timers.target" ];
        timerConfig = {
          OnBootSec = "5min";
          OnUnitInactiveSec = cfg.autoDeploy.interval;
          Unit = "remote-agent-auto-deploy.service";
        };
      };
    })
  ]);
}
