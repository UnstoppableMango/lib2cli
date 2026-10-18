# backend/plan_gen.go is what tdl's go backend makes of backend/plan.tdl.
{ runCommand, tdl }:
runCommand "lib2cli-gen-plan"
  {
    nativeBuildInputs = [ tdl ];
  }
  ''
    cp ${../../tdl.toml} tdl.toml
    mkdir backend
    cp ${../../backend/plan.tdl} backend/plan.tdl
    cp ${../../backend/plan_gen.go} backend/plan_gen.go
    cp ${../../backend/.tdl-output} backend/.tdl-output
    tdl gen --verify backend/plan.tdl
    touch $out
  ''
