# tdl finds the plugin on PATH, completes the handshake, checks the
# fixture's directives against the ones the plugin declares, and writes the
# TypeScript project.
{
  runCommand,
  tdl,
  lib2cli,
}:
runCommand "lib2cli-gen-mdx"
  {
    nativeBuildInputs = [
      tdl
      lib2cli
    ];
  }
  ''
    mkdir -p backend/testdata
    cp -r ${../../lib} lib
    cp -r ${../../backend/testdata/mdx} backend/testdata/mdx
    tdl gen --target lib2cli -o out backend/testdata/mdx/source.tdl
    grep -q '"@mdx-js/mdx": "3.1.0"' out/package.json
    grep -q 'lib.compile(' out/src/main.ts
    touch $out
  ''
