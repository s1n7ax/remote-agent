# remote-agent: Go binary with the Svelte SPA (web/dist) embedded, plus the
# default skills under $out/share/remote-agent/skills.
#
# Updating the fixed-output hashes after dependency changes:
#
#   1. set the hash below to `lib.fakeHash`
#   2. nix build .#remote-agent 2>&1 | grep got:
#   3. paste the `got:` hash back here
#
# `vendorHash` covers go.mod/go.sum, `npmDepsHash` covers web/package-lock.json.
{
  lib,
  buildGoModule,
  buildNpmPackage,
  nodejs,
  # Defaults to the VERSION file (if present) or "dev"; the flake appends the
  # git revision.
  version ? if builtins.pathExists ../VERSION then lib.trim (builtins.readFile ../VERSION) else "dev",
}:
let
  root = ../.;
  has = p: builtins.pathExists (root + "/${p}");

  # Go modules hash of go.mod/go.sum.
  vendorHash = "sha256-IRyIjIhNkSseSaiX9MaCPCvdQKd+uQ+WnT2bgeOvqxU=";

  # npm dependencies hash of web/package-lock.json.
  npmDepsHash = "sha256-aHRU98xuxSWZfCAj2wPMvCijfesHS8KNFBT8fhgVocE=";

  hasWeb = has "web/package.json";
  hasSkills = has "skills";

  frontend = buildNpmPackage {
    pname = "remote-agent-web";
    inherit version nodejs npmDepsHash;
    src = lib.fileset.toSource {
      root = root + "/web";
      fileset = lib.fileset.difference (root + "/web") (
        lib.fileset.unions (
          map lib.fileset.maybeMissing [
            (root + "/web/node_modules")
            (root + "/web/dist")
          ]
        )
      );
    };
    # npm run build -> dist/
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      runHook postInstall
    '';
  };
in
buildGoModule {
  pname = "remote-agent";
  inherit version vendorHash;

  src = lib.fileset.toSource {
    inherit root;
    fileset = lib.fileset.unions (
      [
        (root + "/go.mod")
        (root + "/cmd")
      ]
      ++ map lib.fileset.maybeMissing [
        (root + "/go.sum")
        (root + "/internal")
        (root + "/skills")
        (root + "/VERSION")
        # Keeps `go:embed all:dist` valid when there is no frontend to build.
        (root + "/web/dist/.gitkeep")
      ]
      # Go sources of the web package (embed.go); node_modules/dist excluded.
      ++ lib.optional (has "web") (lib.fileset.fileFilter (f: f.hasExt "go") (root + "/web"))
    );
  };

  subPackages = [ "cmd/remote-agent" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  preBuild = lib.optionalString hasWeb ''
    rm -rf web/dist
    cp -r ${frontend} web/dist
    chmod -R u+w web/dist
  '';

  postInstall = lib.optionalString hasSkills ''
    mkdir -p $out/share/remote-agent
    cp -r skills $out/share/remote-agent/skills
  '';

  passthru = lib.optionalAttrs hasWeb { inherit frontend; };

  meta = {
    description = "Self-hosted, mobile-first board where coding agents work tickets 24/7";
    homepage = "https://github.com/s1n7ax/remote-agent";
    license = lib.licenses.gpl3Plus;
    mainProgram = "remote-agent";
    platforms = lib.platforms.linux;
  };
}
