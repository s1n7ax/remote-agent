# Example host: a 2 GB DigitalOcean droplet running remote-agent, reachable
# only over Tailscale. Build its image with `nix build .#do-image`; see
# docs/deploy.md.
{
  config,
  lib,
  modulesPath,
  ...
}:
{
  imports = [
    # Pulls in virtualisation/digital-ocean-config.nix (disk, grub, metadata,
    # SSH keys from the droplet's metadata) and defines
    # system.build.digitalOceanImage.
    "${modulesPath}/virtualisation/digital-ocean-image.nix"
  ];

  options.droplet.sshKeys = lib.mkOption {
    type = lib.types.listOf lib.types.str;
    default = [ ];
    example = [ "ssh-ed25519 AAAA... you@laptop" ];
    description = ''
      Extra SSH public keys for root. Keys chosen when creating the droplet
      are fetched from DigitalOcean's metadata as well.
    '';
  };

  config = {
    system.stateVersion = "26.11";
    networking.hostName = "remote-agent";

    nixpkgs.config.allowUnfreePredicate = pkg: lib.getName pkg == "claude-code";

    services.remote-agent = {
      enable = true;
      maxParallelRuns = 2;
      # Put the secrets here after first boot (see docs/deploy.md).
      githubTokenFile = "/var/lib/secrets/remote-agent/github-token";
      openrouterKeyFile = "/var/lib/secrets/remote-agent/openrouter-key";
      # publicURL = "https://remote-agent.<tailnet>.ts.net";
      # previewURL = "https://remote-agent.<tailnet>.ts.net:8443";
      tailscale.enable = true;
      # Unattended `tailscale up`; leave null to run `tailscale up` by hand.
      # tailscale.authKeyFile = "/var/lib/secrets/tailscale-authkey";
      autoDeploy = {
        enable = true;
        flake = "github:s1n7ax/remote-agent#droplet";
        interval = "10min";
      };
    };

    # 2 GB RAM: compressed swap absorbs agent peaks and nixos-rebuild builds.
    zramSwap = {
      enable = true;
      memoryPercent = 100;
      algorithm = "zstd";
    };

    # Key-only SSH. Port 22 stays open publicly so the droplet can be reached
    # before Tailscale is up; once it is, set openFirewall = false and use
    # SSH over the tailnet.
    services.openssh = {
      enable = true;
      openFirewall = lib.mkDefault true;
      settings = {
        PasswordAuthentication = false;
        KbdInteractiveAuthentication = false;
        PermitRootLogin = "prohibit-password";
      };
    };
    users.users.root.openssh.authorizedKeys.keys = config.droplet.sshKeys;

    nix.settings = {
      experimental-features = [
        "nix-command"
        "flakes"
      ];
      # Keep builds on a 2 GB box from fanning out.
      max-jobs = 1;
      cores = 1;
    };
    nix.gc = {
      automatic = true;
      dates = "weekly";
      options = "--delete-older-than 14d";
    };
    boot.loader.grub.configurationLimit = 10;
  };
}
