{
  buildGoApplication,
  lib,
  go,
  tdl,
  version,
}:
buildGoApplication {
  pname = "lib2cli";
  inherit version go;

  src = lib.cleanSource ../.;
  modules = ./gomod2nix.toml;

  subPackages = [ "cmd/tdl-gen-lib2cli" ];

  # subPackages limits the default checkPhase to the command, which has no
  # tests. The golden tests lower their fixtures with tdl.
  nativeCheckInputs = [ tdl ];
  checkPhase = ''
    runHook preCheck
    export GOFLAGS=''${GOFLAGS//-trimpath/}
    go test ./...
    runHook postCheck
  '';

  meta = {
    description = "Shell-callable CLI shims for library functions, from TDL models";
    homepage = "https://github.com/UnstoppableMango/lib2cli";
    mainProgram = "tdl-gen-lib2cli";
    license = lib.licenses.mit;
  };
}
