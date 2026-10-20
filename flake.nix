{
  description = "lib2cli - shell-callable CLI shims for library functions, from TDL models";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    systems.url = "github:UnstoppableMango/nix-systems";

    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.inputs.systems.follows = "systems";
    };

    globset = {
      url = "github:pdtpartners/globset";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    # Pinned to the same commit as go.mod's github.com/unstoppablemango/tdl.
    tdl = {
      url = "github:UnstoppableMango/tdl";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.systems.follows = "systems";
      inputs.flake-parts.follows = "flake-parts";
      inputs.treefmt-nix.follows = "treefmt-nix";
      inputs.gomod2nix.follows = "gomod2nix";
    };
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;

      imports = [
        inputs.treefmt-nix.flakeModule
        ./nix
      ];

      _module.args.version = "0.0.0";

      perSystem =
        { pkgs, ... }:
        {
          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go_1_27
              gomod2nix
              gopls
              gnumake
              nixfmt
              nodejs
              tdl
            ];
          };

          treefmt = {
            programs = {
              actionlint.enable = true;
              gofmt.enable = true;
              nixfmt.enable = true;
              yamlfmt = {
                enable = true;
                settings.formatter.retain_line_breaks_single = true;
              };
            };

            settings.global.excludes = [
              "*.tdl"
              "nix/gomod2nix.toml"
              "flake.lock"
            ];
          };
        };
    };
}
