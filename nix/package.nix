{
  buildGoApplication,
  globset,
  importNpmLock,
  lib,
  go,
  nodejs,
  tdl,
  version,
}:
let
  # tsc and Node's types, for checking the TypeScript runtime and building
  # the end-to-end test's shim.
  nodeModules = importNpmLock.buildNodeModules {
    npmRoot = ../backend/typescript;
    inherit nodejs;
  };
in
buildGoApplication {
  pname = "lib2cli";
  inherit version go;

  # The Go sources and the TypeScript runtime they embed, plus the fixtures
  # and the lib2cli module the tests read.
  src = lib.fileset.toSource {
    root = ../.;
    fileset = globset.globs ../. [
      "go.mod"
      "go.sum"
      "**/*.go"
      "backend/typescript/package.json"
      "backend/typescript/package-lock.json"
      "backend/typescript/tsconfig.json"
      "backend/typescript/shim/*.ts"
      "backend/testdata/**"
      "lib/*.tdl"
    ];
  };
  modules = ./gomod2nix.toml;

  subPackages = [ "cmd/tdl-gen-lib2cli" ];

  # subPackages limits the default checkPhase to the command, which has no
  # tests. The golden tests lower their fixtures with tdl, and the
  # TypeScript runtime is checked and tested with node_modules from its
  # lockfile.
  nativeCheckInputs = [
    nodejs
    tdl
  ];
  checkPhase = ''
    runHook preCheck
    export GOFLAGS=''${GOFLAGS//-trimpath/}
    export LIB2CLI_NODE_MODULES=${nodeModules}/node_modules
    go test ./...
    (
      cd backend/typescript
      ln -s $LIB2CLI_NODE_MODULES node_modules
      node node_modules/typescript/bin/tsc
      node --test
    )
    runHook postCheck
  '';

  meta = {
    description = "Shell-callable CLI shims for library functions, from TDL models";
    homepage = "https://github.com/UnstoppableMango/lib2cli";
    mainProgram = "tdl-gen-lib2cli";
    license = lib.licenses.mit;
  };
}
