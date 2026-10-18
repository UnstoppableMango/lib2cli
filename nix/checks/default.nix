{ pkgs }:
{
  gen-mdx = pkgs.callPackage ./gen-mdx.nix { };
  gen-plan = pkgs.callPackage ./gen-plan.nix { };
}
