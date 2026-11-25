# NixOS VM test: the module on a 2 GB machine, bound to loopback only, small
# idle RSS, bubblewrap usable by the service user (also under the unit's own
# hardening), and the auto-deploy rollback path.
{ self, pkgs }:
let
  inherit (pkgs) lib;

  # Fakes for the auto-deploy script: they simulate a deploy without a real
  # flake or nixos-rebuild.
  fakeNix = pkgs.writeShellScript "fake-nix" ''
    # nix flake metadata --json --refresh <ref>
    echo "{\"revision\":\"$(cat /tmp/fake-rev)\",\"url\":\"path:/fake?rev=$(cat /tmp/fake-rev)\"}"
  '';
  fakeNixosVersion = pkgs.writeShellScript "fake-nixos-version" "echo old-rev";
  fakeNixosRebuild = pkgs.writeShellScript "fake-nixos-rebuild" ''
    set -eu
    echo "fake-nixos-rebuild $*" >> /tmp/fake-rebuild.log
    if [ "$2" = --rollback ]; then
      ln -sfn /fake/gen-1 /tmp/fake-profile
      systemctl start remote-agent.service
      touch /tmp/rolled-back
    else
      ln -sfn /fake/gen-2-$(cat /tmp/fake-rev) /tmp/fake-profile
      if [ -e /tmp/break-deploy ]; then
        # The "new generation" does not come up.
        systemctl stop remote-agent.service
      fi
    fi
  '';
in
pkgs.testers.runNixOSTest {
  name = "remote-agent";

  nodes.server =
    { config, ... }:
    {
      imports = [ self.nixosModules.default ];
      virtualisation.memorySize = 2048;
      virtualisation.cores = 2;

      services.remote-agent = {
        enable = true;
        githubTokenFile = "${pkgs.writeText "github-token" "test-token"}";
        tailscale.serve = false;
        autoDeploy = {
          enable = true;
          flake = "path:/fake#server";
        };
      };
      # Only the test drives the timer.
      systemd.timers.remote-agent-auto-deploy.enable = false;

      # Same sandboxing as the app, running bwrap the way the app will.
      systemd.services.bwrap-probe = {
        path = [ config.services.remote-agent.bubblewrapPackage ];
        serviceConfig =
          lib.filterAttrs (
            n: _:
            !lib.elem n [
              "ExecStart"
              "ExecStartPre"
              "Restart"
              "RestartSec"
              "LoadCredential"
            ]
          ) config.systemd.services.remote-agent.serviceConfig
          // {
            Type = "oneshot";
            ExecStart = pkgs.writeShellScript "bwrap-probe" ''
              set -eux
              env=$(${pkgs.jq}/bin/jq -r .sandboxEnv /var/lib/remote-agent/config.json)
              mkdir -p /var/lib/remote-agent/probe
              bwrap --unshare-all --share-net --die-with-parent \
                --ro-bind /nix/store /nix/store \
                --bind /var/lib/remote-agent/probe /work --chdir /work \
                --proc /proc --dev /dev --tmpfs /tmp \
                --setenv PATH "$env/bin" --setenv HOME /work \
                "$env/bin/bash" -c 'git --version && echo hello > /work/out && id -u'
              test "$(cat /var/lib/remote-agent/probe/out)" = hello
              # Chromium (as used by playwright-mcp) inside the sandbox.
              chrome=$(echo ${pkgs.playwright-driver.browsers}/chromium-*/chrome-linux*/chrome)
              bwrap --unshare-all --die-with-parent \
                --ro-bind /nix/store /nix/store \
                --bind /var/lib/remote-agent/probe /work --chdir /work \
                --proc /proc --dev /dev --tmpfs /tmp --tmpfs /dev/shm \
                --setenv HOME /work \
                "$chrome" --headless --disable-gpu --dump-dom \
                  'data:text/html,<p id=x>sandboxed-chromium-ok</p>' > /var/lib/remote-agent/probe/dom
              grep -q sandboxed-chromium-ok /var/lib/remote-agent/probe/dom
            '';
          };
      };

      environment.systemPackages = [
        pkgs.jq
        pkgs.bubblewrap
      ];
      environment.etc = {
        "fake/nix".source = fakeNix;
        "fake/nixos-version".source = fakeNixosVersion;
        "fake/nixos-rebuild".source = fakeNixosRebuild;
      };
    };

  nodes.client = {
    environment.systemPackages = [ pkgs.curl ];
  };

  testScript =
    { nodes, ... }:
    let
      deploy = lib.getExe nodes.server.services.remote-agent.autoDeploy.package;
      bwrap = lib.getExe pkgs.bubblewrap;
      env = lib.concatStringsSep " " [
        "RA_FLAKE=path:/fake#server"
        "RA_NIX=/etc/fake/nix"
        "RA_NIXOS_VERSION=/etc/fake/nixos-version"
        "RA_NIXOS_REBUILD=/etc/fake/nixos-rebuild"
        "RA_SYSTEM_PROFILE=/tmp/fake-profile"
        "RA_STATE_DIR=/tmp/deploy-state"
        "RA_HEALTH_RETRIES=5"
        "RA_HEALTH_DELAY=1"
        "RA_POLL_SECS=1"
      ];
    in
    ''
      start_all()

      with subtest("service is active and healthy on loopback"):
          server.wait_for_unit("remote-agent.service")
          server.wait_for_open_port(7700)
          code = server.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:7700/healthz")
          assert code == "200", f"/healthz returned {code}"
          server.succeed("remote-agent healthcheck --url http://127.0.0.1:7700")
          listeners = server.succeed("ss -ltnH '( sport = :7700 or sport = :7701 )' | awk '{print $4}'").split()
          print("listeners:", listeners)
          assert listeners and all(l.startswith("127.0.0.1:") for l in listeners), listeners

      with subtest("config.json follows the Config contract"):
          server.succeed("jq -e '.listen == \"127.0.0.1:7700\" and .mcpListen == \"127.0.0.1:7701\" and .maxParallelRuns == 2' /var/lib/remote-agent/config.json")
          server.succeed("jq -e '.githubTokenFile == \"/run/credentials/remote-agent.service/github-token\"' /var/lib/remote-agent/config.json")
          server.succeed("test -x \"$(jq -r .mcpServers.playwright.command /var/lib/remote-agent/config.json)\"")
          server.succeed("test -x \"$(jq -r .claudeBin /var/lib/remote-agent/config.json)\"")
          server.succeed("test -x \"$(jq -r .piBin /var/lib/remote-agent/config.json)\"")
          server.succeed("test -x \"$(jq -r .sandboxEnv /var/lib/remote-agent/config.json)/bin/git\"")
          server.succeed("systemctl show -p Slice --value remote-agent.service | grep -qx remote-agent.slice")
          server.succeed("systemctl show -p MemoryHigh --value remote-agent.slice | grep -qx 1610612736")

      with subtest("the app is not reachable from another machine"):
          client.wait_for_unit("multi-user.target")
          client.succeed("ping -c1 -W5 server")
          for port in (7700, 7701, 7702):
              client.fail(f"curl -sS --max-time 5 http://server:{port}/healthz")

      with subtest("idle RSS is under 150 MB"):
          pid = server.succeed("systemctl show -p MainPID --value remote-agent.service").strip()
          rss_kb = int(server.succeed(f"awk '/^VmRSS/ {{print $2}}' /proc/{pid}/status").strip())
          print(f"remote-agent idle VmRSS: {rss_kb} kB ({rss_kb / 1024:.1f} MB)")
          assert rss_kb < 150 * 1024, f"idle RSS {rss_kb} kB >= 150 MB"

      with subtest("bubblewrap works for the service user"):
          server.succeed("sudo -u remote-agent ${bwrap} --unshare-all --ro-bind /nix/store /nix/store --proc /proc --dev /dev ${pkgs.coreutils}/bin/true")
          server.succeed("systemctl start bwrap-probe.service")
          print(server.succeed("journalctl -u bwrap-probe.service --no-pager | tail -n 5"))

      with subtest("auto-deploy: up to date is a no-op"):
          server.succeed("echo old-rev > /tmp/fake-rev; ln -sfn /fake/gen-1 /tmp/fake-profile")
          out = server.succeed("${env} ${deploy} 2>&1")
          print(out)
          assert "up to date" in out

      with subtest("auto-deploy: failing health check rolls back"):
          server.succeed("echo bad-rev > /tmp/fake-rev; touch /tmp/break-deploy")
          out = server.fail("${env} ${deploy} 2>&1")
          print(out)
          assert "draining" in out and "app is idle" in out
          assert "rolling back" in out and "rolled back to the previous generation" in out
          assert "app healthy after rollback" in out
          server.succeed("test -e /tmp/rolled-back")
          server.succeed("grep -x 'fake-nixos-rebuild switch --rollback' /tmp/fake-rebuild.log")
          server.wait_for_unit("remote-agent.service")
          server.succeed("curl -fsS http://127.0.0.1:7700/api/idle | jq -e '.draining == false'")
          # The failed revision is not retried.
          out = server.succeed("${env} ${deploy} 2>&1")
          print(out)
          assert "skipping bad-rev" in out

      with subtest("auto-deploy: healthy deploy keeps the new generation and undrains"):
          server.succeed("rm /tmp/break-deploy /tmp/rolled-back; echo good-rev > /tmp/fake-rev")
          out = server.succeed("${env} ${deploy} 2>&1")
          print(out)
          assert "deployed good-rev" in out and "undrained" in out
          server.succeed("test \"$(readlink /tmp/fake-profile)\" = /fake/gen-2-good-rev")
          server.succeed("grep -q 'switch --flake path:/fake?rev=good-rev#server' /tmp/fake-rebuild.log")
          server.succeed("test ! -e /tmp/rolled-back")
          server.succeed("curl -fsS http://127.0.0.1:7700/api/idle | jq -e '.draining == false'")

      with subtest("auto-deploy unit is wired"):
          server.succeed("systemctl cat remote-agent-auto-deploy.service | grep -q RA_FLAKE=path:/fake#server")
    '';
}
