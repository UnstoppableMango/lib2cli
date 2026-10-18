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
      checks = {
        inherit (pkgs) lib2cli;
      }
      // import ./checks { inherit pkgs; };
    };
}
