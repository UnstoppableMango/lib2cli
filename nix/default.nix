{
  inputs,
  version,
  ...
}:
let
  overlay = import ./overlay.nix {
    inherit version;
    inherit (inputs.nixpkgs) lib;
    inherit (inputs) globset gomod2nix;
  };
in
{
  flake.overlays.default = overlay;

  perSystem =
    { system, pkgs, ... }:
    {
      _module.args.pkgs = import inputs.nixpkgs {
        inherit system;
        overlays = [
          inputs.tdl.overlays.default
          overlay
        ];
      };

      packages = {
        inherit (pkgs) lib2cli;
        default = pkgs.lib2cli;
      };

      # Building the package runs the golden tests.
      checks.lib2cli = pkgs.lib2cli;

      # tdl finds the plugin on PATH, completes the handshake, and checks
      # the fixture's directives against the ones the plugin declares.
      checks.gen-mdx =
        pkgs.runCommand "lib2cli-gen-mdx"
          {
            nativeBuildInputs = [
              pkgs.tdl
              pkgs.lib2cli
            ];
          }
          ''
            mkdir -p backend/testdata
            cp -r ${../lib} lib
            cp -r ${../backend/testdata/mdx} backend/testdata/mdx
            tdl gen --target lib2cli -o out backend/testdata/mdx/source.tdl 2>&1 | tee log
            grep -q 'planned 1 command(s) for @mdx-js/mdx' log
            touch $out
          '';
    };
}
