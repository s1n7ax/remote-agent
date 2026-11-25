{
  description = "remote-agent: a self-hosted, mobile-first board where coding agents work tickets 24/7";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      inherit (nixpkgs) lib;
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      pkgsFor =
        system:
        import nixpkgs {
          inherit system;
          # claude-code is unfree; nothing else is allowed.
          config.allowUnfreePredicate = pkg: lib.getName pkg == "claude-code";
        };
      forAll = f: lib.genAttrs systems (system: f (pkgsFor system));

      baseVersion =
        if builtins.pathExists ./VERSION then lib.trim (builtins.readFile ./VERSION) else "0.0.0";
      version = "${baseVersion}+${self.shortRev or self.dirtyShortRev or "dirty"}";
    in
    {
      packages = forAll (
        pkgs:
        rec {
          remote-agent = pkgs.callPackage ./nix/package.nix { inherit version; };
          default = remote-agent;
        }
        // lib.optionalAttrs (pkgs.stdenv.hostPlatform.system == "x86_64-linux") {
          # DigitalOcean custom image (qcow2.gz) of the example droplet host.
          do-image = self.nixosConfigurations.droplet.config.system.build.digitalOceanImage;
        }
      );

      nixosModules.default = import ./nix/module.nix self;

      nixosConfigurations.droplet = lib.nixosSystem {
        modules = [
          self.nixosModules.default
          ./nix/hosts/droplet.nix
          {
            nixpkgs.hostPlatform = "x86_64-linux";
            # Compared with the flake's latest revision by auto-deploy.
            system.configurationRevision = self.rev or self.dirtyRev or null;
          }
        ];
      };

      checks = forAll (
        pkgs:
        let
          remote-agent = self.packages.${pkgs.stdenv.hostPlatform.system}.remote-agent;
        in
        {
          inherit remote-agent;
          vm = import ./nix/tests/vm.nix { inherit self pkgs; };

          # go vet + go test with the same sources and modules as the package.
          go = remote-agent.overrideAttrs (_: {
            pname = "remote-agent-go-check";
            doCheck = true;
            checkPhase = ''
              runHook preCheck
              go vet ./...
              go test ./...
              runHook postCheck
            '';
            installPhase = "touch $out";
            postInstall = "";
          });

          nixfmt = pkgs.runCommand "nixfmt-check" { nativeBuildInputs = [ pkgs.nixfmt ]; } ''
            cd ${self}
            find . -name '*.nix' -print0 | xargs -0 nixfmt --check
            touch $out
          '';
        }
      );

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            govulncheck
            nodejs
            sqlite
            bubblewrap
            git
            playwright-driver.browsers
            nixfmt
            actionlint
            shellcheck
          ];
          PLAYWRIGHT_BROWSERS_PATH = "${pkgs.playwright-driver.browsers}";
          PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS = "true";
        };
      });

      formatter = forAll (pkgs: pkgs.nixfmt-tree);
    };
}
