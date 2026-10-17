{
  lib,
  gomod2nix,
  version,
}:
lib.composeExtensions gomod2nix.overlays.default (
  final: _prev: {
    lib2cli = final.callPackage ./package.nix {
      inherit version;
      go = final.go_1_27;
    };
  }
)
