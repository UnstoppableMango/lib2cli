import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough, Readable } from "node:stream";
import { test } from "node:test";
import {
  type Command,
  type IO,
  type Type,
  execute,
  exitError,
  exitOK,
  exitUsage,
  writeBytes,
  writeJSON,
  writeStream,
  writeString,
  writeText,
} from "./shim.ts";

const str: Type = { kind: "string" };
const num: Type = { kind: "int" };
const boolean: Type = { kind: "bool" };
const stream: Type = { kind: "stream" };
const list = (elem: Type): Type => ({ kind: "list", elem });
const dict = (elem: Type): Type => ({ kind: "map", elem });
const option = (elem: Type): Type => ({ kind: "option", elem });

async function text(r: Readable): Promise<string> {
  let s = "";
  for await (const c of r) s += c;
  return s;
}

// commands are written the way the generator writes them.
const commands: Command[] = [
  {
    name: "greet",
    params: [
      { name: "name", flag: "name", type: str },
      { name: "excited", flag: "excited", type: boolean },
    ],
    async call(a, io) {
      const res = "Hello, " + (a[0] as string) + ((a[1] as boolean) ? "!" : "");
      await writeString(io, res);
    },
  },
  {
    name: "divide",
    params: [
      { name: "a", flag: "a", type: num },
      { name: "b", flag: "b", type: { kind: "float" } },
    ],
    async call(a, io) {
      const res = await (async (x: number, y: number) => {
        if (y === 0) throw new Error("division by zero");
        return x / y;
      })(a[0] as number, a[1] as number);
      await writeJSON(io, res);
    },
  },
  {
    name: "tag",
    params: [
      { name: "name", flag: "name", type: str },
      { name: "tags", flag: "tags", type: list(str) },
      { name: "labels", flag: "labels", type: dict(num) },
      { name: "note", flag: "note", type: option(str) },
      { name: "raw_bytes", flag: "raw-bytes", type: { kind: "bytes" } },
    ],
    async call(a, io) {
      const req = {
        name: a[0] as string,
        tags: a[1] as string[],
        labels: a[2] as Record<string, number>,
        note: a[3] as string | undefined,
        raw_bytes: a[4] as Uint8Array,
      };
      await writeJSON(io, { ...req, raw_bytes: [...req.raw_bytes] });
    },
  },
  {
    name: "level",
    params: [
      { name: "options", flag: "options", type: option({ kind: "object" }) },
      { name: "levels", flag: "levels", type: list({ kind: "object" }) },
    ],
    async call(a, io) {
      const opts = a[0] as { level: number } | undefined;
      const levels = a[1] as { level: number }[];
      await writeJSON(io, levels.reduce((n, l) => n + l.level, opts?.level ?? 0));
    },
  },
  {
    name: "upper",
    params: [{ name: "in", flag: "in", type: stream }],
    async call(a, io) {
      const s = await text(a[0] as Readable);
      await writeStream(io, Readable.from([s.toUpperCase()]));
    },
  },
  {
    name: "vfile",
    params: [],
    async call(_a, io) {
      await writeText(io, { messages: ["unused import"], toString: () => "compiled" });
    },
  },
  {
    name: "bytes",
    params: [],
    async call(_a, io) {
      await writeBytes(io, new TextEncoder().encode("raw"));
    },
  },
  {
    name: "none",
    params: [],
    async call(_a, io) {
      await writeString(io, undefined);
    },
  },
];

async function run(cmds: Command[], args: string[], stdin = "") {
  const stdout = new PassThrough();
  const stderr = new PassThrough();
  const io: IO = { stdin: Readable.from([stdin]), stdout, stderr };
  const code = await execute("tool", cmds, args, io);
  stdout.end();
  stderr.end();
  return { code, out: await text(stdout), err: await text(stderr) };
}

const dir = mkdtempSync(join(tmpdir(), "shim-"));
const file = join(dir, "in.txt");
writeFileSync(file, "a\nb\n");

const cases: { name: string; args: string[]; stdin?: string; code?: number; out?: string; err?: string }[] = [
  { name: "raw string", args: ["greet", "--name", "Ada"], out: "Hello, Ada" },
  { name: "bare bool", args: ["greet", "--name=Ada", "--excited"], out: "Hello, Ada!" },
  { name: "json mode", args: ["greet", "--json"], stdin: `{"name":"Ada","excited":true}`, out: "Hello, Ada!" },
  { name: "missing flag", args: ["greet"], code: exitUsage, err: "tool greet: --name is required\n" },
  { name: "repeated scalar", args: ["greet", "--name=a", "--name=b"], code: exitUsage, err: "tool greet: --name is given more than once\n" },
  { name: "unknown flag", args: ["greet", "--nope"], code: exitUsage, err: "Unknown option '--nope'" },
  { name: "extra argument", args: ["greet", "--name=a", "x"], code: exitUsage, err: "Unexpected argument 'x'" },
  { name: "json missing field", args: ["greet", "--json"], stdin: `{"excited":true}`, code: exitUsage, err: "tool greet: name is required\n" },
  { name: "json null field", args: ["greet", "--json"], stdin: `{"name":null}`, code: exitUsage, err: "name is required" },
  { name: "json wrong type", args: ["greet", "--json"], stdin: `{"name":1}`, code: exitUsage, err: "tool greet: name: 1 is not a string\n" },
  { name: "json not an object", args: ["greet", "--json"], stdin: `[]`, code: exitUsage, err: "not a JSON object" },
  { name: "bad json", args: ["greet", "--json"], stdin: `{`, code: exitUsage, err: "reading --json arguments:" },
  { name: "json and flags", args: ["greet", "--json", "--name=a"], code: exitUsage, err: "--name cannot be combined with --json" },
  { name: "int", args: ["divide", "--a=7", "--b=2"], out: "3.5\n" },
  { name: "error exits", args: ["divide", "--a=7", "--b=0"], code: exitError, err: "tool divide: division by zero\n" },
  { name: "bad number", args: ["divide", "--a=x", "--b=1"], code: exitUsage, err: `tool divide: --a: "x" is not a number\n` },
  { name: "not an integer", args: ["divide", "--a=1.5", "--b=1"], code: exitUsage, err: "tool divide: --a: 1.5 is not an integer\n" },
  { name: "json not an integer", args: ["divide", "--json"], stdin: `{"a":1.5,"b":1}`, code: exitUsage, err: "tool divide: a: 1.5 is not an integer\n" },
  {
    name: "lists, maps, options and bytes",
    args: ["tag", "--name=n", "--tags=a", "--tags=b", "--labels=k=1", "--labels=j=2", "--raw-bytes=hi"],
    out: `{"name":"n","tags":["a","b"],"labels":{"k":1,"j":2},"raw_bytes":[104,105]}\n`,
  },
  {
    name: "the same from json",
    args: ["tag", "--json"],
    stdin: `{"name":"n","note":"x","raw_bytes":"hi","tags":["a"]}`,
    out: `{"name":"n","tags":["a"],"labels":{},"note":"x","raw_bytes":[104,105]}\n`,
  },
  { name: "bad map flag", args: ["tag", "--name=n", "--raw-bytes=", "--labels=k"], code: exitUsage, err: `--labels: "k" is not key=value` },
  { name: "bad map value", args: ["tag", "--name=n", "--raw-bytes=", "--labels=k=v"], code: exitUsage, err: `--labels: "v" is not a number` },
  { name: "json list element", args: ["tag", "--json"], stdin: `{"name":"n","raw_bytes":"","tags":[1]}`, code: exitUsage, err: "tags[0]: 1 is not a string" },
  { name: "objects", args: ["level", `--options={"level":1}`, `--levels={"level":2}`, `--levels={"level":3}`], out: "6\n" },
  { name: "objects from json", args: ["level", "--json"], stdin: `{"levels":[{"level":2},{"level":3}]}`, out: "5\n" },
  { name: "absent option", args: ["level"], out: "0\n" },
  { name: "bad object", args: ["level", "--options={"], code: exitUsage, err: `--options: "{" is not JSON` },
  { name: "not an object", args: ["level", "--options=[]"], code: exitUsage, err: "--options: [] is not an object" },
  { name: "stream from stdin", args: ["upper", "--in=-"], stdin: "shout", out: "SHOUT" },
  { name: "stream from file", args: ["upper", "--in", file], out: "A\nB\n" },
  { name: "missing file", args: ["upper", "--in", join(dir, "nope")], code: exitUsage, err: "tool upper: --in: ENOENT" },
  { name: "stream in json mode", args: ["upper", "--json"], stdin: `{"in":"-"}`, code: exitUsage, err: "stdin holds the --json arguments" },
  { name: "text writes vfile messages", args: ["vfile"], out: "compiled", err: "warning: unused import\n" },
  { name: "bytes", args: ["bytes"], out: "raw" },
  { name: "absent result", args: ["none"], out: "" },
  { name: "no command", args: [], code: exitUsage, err: "usage:" },
  { name: "help", args: ["--help"], out: "usage: tool <command>" },
  { name: "command help", args: ["greet", "--help"], out: "usage: tool greet [flags]\n\n  --name\tstring\n  --excited\tbool\n" },
  { name: "unknown command", args: ["nope"], code: exitUsage, err: `tool: unknown command "nope"` },
];

for (const c of cases) {
  test(c.name, async () => {
    const got = await run(commands, c.args, c.stdin);
    assert.equal(got.code, c.code ?? exitOK, `stderr: ${got.err}`);
    if (c.out !== undefined) assert.ok(got.out.startsWith(c.out) && (c.out !== "" || got.out === ""), `stdout ${JSON.stringify(got.out)}`);
    assert.ok(got.err.includes(c.err ?? ""), `stderr ${JSON.stringify(got.err)}`);
    if (c.err === undefined) assert.equal(got.err, "");
  });
}

test("single command", async () => {
  const got = await run(commands.slice(0, 1), ["--name", "Ada"]);
  assert.deepEqual(got, { code: exitOK, out: "Hello, Ada", err: "" });
});
