{
  description = "remote-agent: a self-hosted, mobile-first board where coding agents work tickets 24/7";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        remote-agent = pkgs.callPackage ./nix/package.nix { };
        default = remote-agent;
      });

      nixosModules.default = import ./nix/module.nix self;

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
          ];
          PLAYWRIGHT_BROWSERS_PATH = "${pkgs.playwright-driver.browsers}";
          PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS = "true";
        };
      });

      formatter = forAll (pkgs: pkgs.nixfmt-tree);
    };
}
