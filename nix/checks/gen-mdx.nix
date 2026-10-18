# tdl finds the plugin on PATH, completes the handshake, and checks the
# fixture's directives against the ones the plugin declares.
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
    tdl gen --target lib2cli -o out backend/testdata/mdx/source.tdl 2>&1 | tee log
    grep -q 'planned 1 command(s) for @mdx-js/mdx' log
    touch $out
  ''
