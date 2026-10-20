// The runtime every generated TypeScript shim carries. lib2cli copies this
// file into the generated project beside main.ts, which lists a call per
// command.
//
// The runtime only knows lib2cli's neutral types. It parses a command's
// flags with node:util's parseArgs, or reads them as one JSON object from
// stdin with --json, checks each argument against its parameter's type,
// and hands the arguments to the command's call in order. Each call is
// written by lib2cli with the parameter types the model gives, so tsc
// checks them, and the result type, against the library's own
// declarations when the project is built.

import { createReadStream, openSync } from "node:fs";
import type { Readable, Writable } from "node:stream";
import { parseArgs } from "node:util";

/** A parameter type in lib2cli's neutral set. */
export type Type = {
  kind:
    | "string"
    | "int"
    | "float"
    | "bool"
    | "bytes"
    | "path"
    | "stream"
    | "text"
    | "list"
    | "map"
    | "object"
    | "option";
  elem?: Type;
};

/** One field of a command's request, set by a flag. */
export type Param = { name: string; flag: string; type: Type };

/** One library function, called as a subcommand. */
export type Command = {
  name: string;
  params: Param[];
  /** call calls the function with args, in parameter order, and writes its result. */
  call(args: unknown[], io: IO): Promise<void>;
};

/** Where a command reads and writes. */
export type IO = { stdin: Readable; stdout: Writable; stderr: Writable };

// Exit codes.
export const exitOK = 0;
export const exitError = 1; // the function failed
export const exitUsage = 2; // the command line was wrong

/** UsageError is a command line the runtime cannot use. */
class UsageError extends Error {}

/** run executes the process's command line against cmds and sets its exit code. */
export async function run(bin: string, cmds: Command[]): Promise<void> {
  const io = { stdin: process.stdin, stdout: process.stdout, stderr: process.stderr };
  process.exitCode = await execute(bin, cmds, process.argv.slice(2), io);
}

/** execute runs one command line and returns its exit code. */
export async function execute(bin: string, cmds: Command[], args: string[], io: IO): Promise<number> {
  if (cmds.length === 1) {
    return executeCommand(cmds[0], bin, args, io);
  }
  const [name, ...rest] = args;
  if (name === undefined) {
    io.stderr.write(usage(bin, cmds));
    return exitUsage;
  }
  if (name === "-h" || name === "--help" || name === "help") {
    io.stdout.write(usage(bin, cmds));
    return exitOK;
  }
  const cmd = cmds.find((c) => c.name === name);
  if (cmd === undefined) {
    io.stderr.write(`${bin}: unknown command ${JSON.stringify(name)}\n${usage(bin, cmds)}`);
    return exitUsage;
  }
  return executeCommand(cmd, `${bin} ${cmd.name}`, rest, io);
}

/** usage lists the subcommands. */
function usage(bin: string, cmds: Command[]): string {
  const names = cmds.map((c) => `  ${c.name}\n`).join("");
  return `usage: ${bin} <command> [flags]\n\ncommands:\n${names}\nRun ${bin} <command> --help for a command's flags.\n`;
}

/** flagUsage lists a command's flags. */
function flagUsage(name: string, params: Param[]): string {
  const flags = params.map((p) => `  --${p.flag}\t${describe(p.type)}\n`).join("");
  return `usage: ${name} [flags]\n\n${flags}  --json\tread the arguments as one JSON object from stdin\n`;
}

/** executeCommand parses one command's flags, calls its function, and writes the result. */
async function executeCommand(cmd: Command, name: string, argv: string[], io: IO): Promise<number> {
  let args: unknown[];
  try {
    args = await parse(cmd.params, argv, io.stdin);
  } catch (err) {
    if (err === help) {
      io.stdout.write(flagUsage(name, cmd.params));
      return exitOK;
    }
    io.stderr.write(`${name}: ${message(err)}\n`);
    return err instanceof UsageError ? exitUsage : exitError;
  }
  try {
    await cmd.call(args, io);
    return exitOK;
  } catch (err) {
    io.stderr.write(`${name}: ${message(err)}\n`);
    return exitError;
  }
}

/** help is thrown by parse for --help. */
const help = Symbol("help");

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * parse reads a command's arguments from its flags, or from stdin with
 * --json, and returns them in parameter order.
 */
async function parse(params: Param[], argv: string[], stdin: Readable): Promise<unknown[]> {
  // Every flag but a bool collects each occurrence, so a scalar given
  // twice is an error rather than silently the last.
  const options: Record<string, { type: "string" | "boolean"; multiple?: boolean }> = {
    json: { type: "boolean" },
    help: { type: "boolean" },
  };
  for (const p of params) {
    options[p.flag] = isBool(p.type) ? { type: "boolean" } : { type: "string", multiple: true };
  }
  let values: Record<string, unknown>;
  try {
    ({ values } = parseArgs({ args: argv, options, strict: true, allowPositionals: false }));
  } catch (err) {
    throw new UsageError(message(err));
  }
  if (values.help) {
    throw help;
  }

  if (values.json) {
    const given = params.find((p) => values[p.flag] !== undefined);
    if (given) {
      throw new UsageError(`--${given.flag} cannot be combined with --json`);
    }
    let obj: unknown;
    try {
      obj = JSON.parse(await text(stdin));
    } catch (err) {
      throw new UsageError(`reading --json arguments: ${message(err)}`);
    }
    if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
      throw new UsageError("reading --json arguments: not a JSON object");
    }
    const fields = obj as Record<string, unknown>;
    const inputs = new Inputs(stdin, true);
    return params.map((p) => decode(p.type, fields[p.name], p.name, inputs));
  }

  const inputs = new Inputs(stdin, false);
  return params.map((p) => {
    const where = `--${p.flag}`;
    const v = values[p.flag];
    if (typeof v === "boolean") {
      return v;
    }
    const set = (v ?? []) as string[];
    if (set.length > 1 && !isRepeated(p.type)) {
      throw new UsageError(`${where} is given more than once`);
    }
    return decode(p.type, set.length === 0 ? undefined : fromFlag(p.type, set, where), where, inputs);
  });
}

/**
 * fromFlag turns a flag's occurrences into the value --json would give:
 * a list's into an array, a map's key=value pairs into an object, and a
 * number or JSON literal into its value.
 */
function fromFlag(t: Type, set: string[], where: string): unknown {
  switch (t.kind) {
    case "option":
      return fromFlag(t.elem!, set, where);
    case "list":
      return set.map((s) => literal(t.elem!, s, where));
    case "map":
      return Object.fromEntries(
        set.map((s) => {
          const i = s.indexOf("=");
          if (i < 0) {
            throw new UsageError(`${where}: ${JSON.stringify(s)} is not key=value`);
          }
          return [s.slice(0, i), literal(t.elem!, s.slice(i + 1), where)];
        }),
      );
  }
  return literal(t, set[0], where);
}

/** literal reads one flag occurrence. A list, map or object is given as JSON. */
function literal(t: Type, s: string, where: string): unknown {
  switch (t.kind) {
    case "option":
      return literal(t.elem!, s, where);
    case "int":
    case "float": {
      const n = s.trim() === "" ? NaN : Number(s);
      if (Number.isNaN(n)) {
        throw new UsageError(`${where}: ${JSON.stringify(s)} is not a number`);
      }
      return n;
    }
    case "bool":
      if (s !== "true" && s !== "false") {
        throw new UsageError(`${where}: ${JSON.stringify(s)} is not a bool`);
      }
      return s === "true";
    case "object":
    case "list":
    case "map":
      try {
        return JSON.parse(s);
      } catch {
        throw new UsageError(`${where}: ${JSON.stringify(s)} is not JSON`);
      }
  }
  return s;
}

/**
 * decode checks one argument against its type and converts what the
 * function takes differently from JSON: bytes become a Uint8Array of the
 * string's UTF-8, and a stream, given as a file name or - for stdin, is
 * opened. A missing or null bool, list or map is its empty value, and a
 * missing option is undefined; any other missing argument is an error.
 */
function decode(t: Type, v: unknown, where: string, inputs: Inputs): unknown {
  if (v === undefined || v === null) {
    switch (t.kind) {
      case "option":
        return undefined;
      case "bool":
        return false;
      case "list":
        return [];
      case "map":
        return {};
    }
    throw new UsageError(`${where} is required`);
  }
  const wrong = () => new UsageError(`${where}: ${JSON.stringify(v)} is not ${article(t)}`);
  switch (t.kind) {
    case "option":
      return decode(t.elem!, v, where, inputs);
    case "string":
    case "path":
    case "text":
      if (typeof v !== "string") throw wrong();
      return v;
    case "bytes":
      if (typeof v !== "string") throw wrong();
      return new TextEncoder().encode(v);
    case "stream":
      if (typeof v !== "string") throw wrong();
      return inputs.open(v, where);
    case "int":
      if (!Number.isSafeInteger(v)) throw wrong();
      return v;
    case "float":
      if (typeof v !== "number") throw wrong();
      return v;
    case "bool":
      if (typeof v !== "boolean") throw wrong();
      return v;
    case "object":
      if (typeof v !== "object" || Array.isArray(v)) throw wrong();
      return v;
    case "list":
      if (!Array.isArray(v)) throw wrong();
      return v.map((e, i) => decode(t.elem!, e, `${where}[${i}]`, inputs));
    case "map":
      if (typeof v !== "object" || Array.isArray(v)) throw wrong();
      return Object.fromEntries(
        Object.entries(v).map(([k, e]) => [k, decode(t.elem!, e, `${where}.${k}`, inputs)]),
      );
  }
}

/** article names a type with its article, as an error message does. */
function article(t: Type): string {
  switch (t.kind) {
    case "int":
      return "an integer";
    case "float":
      return "a number";
    case "object":
      return "an object";
    case "list":
      return "a list";
    case "map":
      return "an object of key/value pairs";
    case "option":
      return article(t.elem!);
    case "stream":
    case "path":
      return "a file name";
  }
  return `a ${t.kind}`;
}

/** isBool says whether a flag is given bare. */
function isBool(t: Type): boolean {
  return t.kind === "option" ? isBool(t.elem!) : t.kind === "bool";
}

/** isRepeated says whether a flag may be given more than once. */
function isRepeated(t: Type): boolean {
  return t.kind === "option" ? isRepeated(t.elem!) : t.kind === "list" || t.kind === "map";
}

/** describe is a flag's help text. */
function describe(t: Type): string {
  switch (t.kind) {
    case "list":
      return `${describe(t.elem!)}, repeatable`;
    case "map":
      return `key=${describe(t.elem!)}, repeatable`;
    case "option":
      return `${describe(t.elem!)}, optional`;
    case "object":
      return "JSON object";
    case "stream":
      return "file to read, - for stdin";
    case "path":
      return "file path";
  }
  return t.kind;
}

/** Inputs opens the files stream parameters name. */
class Inputs {
  #stdin: Readable;
  #stdinRead: boolean; // stdin held the --json arguments

  constructor(stdin: Readable, stdinRead: boolean) {
    this.#stdin = stdin;
    this.#stdinRead = stdinRead;
  }

  /** open opens a stream parameter: the file name names, or stdin for -. */
  open(name: string, where: string): Readable {
    if (name !== "-") {
      // Opened now, so a file that is not there is a usage error rather
      // than the function's.
      try {
        return createReadStream("", { fd: openSync(name, "r") });
      } catch (err) {
        throw new UsageError(`${where}: ${message(err)}`);
      }
    }
    if (this.#stdinRead) {
      throw new UsageError(`${where}: stdin holds the --json arguments, so a stream cannot read it`);
    }
    this.#stdinRead = true;
    return this.#stdin;
  }
}

/** text reads a stream whole as UTF-8. */
async function text(r: AsyncIterable<string | Uint8Array>): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const c of r) {
    chunks.push(typeof c === "string" ? Buffer.from(c) : Buffer.from(c.buffer, c.byteOffset, c.byteLength));
  }
  return Buffer.concat(chunks).toString("utf8");
}

// The writers below are what generated calls write results with. Their
// parameter types are what tsc checks a function's result against. An
// option's absent value writes nothing, or null as JSON.

/** writeString writes a string result raw. */
export async function writeString(io: IO, s: string | null | undefined): Promise<void> {
  if (s != null) await write(io.stdout, s);
}

/**
 * writeText writes a result raw as String(x) does. A VFile's messages go
 * to stderr as warnings.
 */
export async function writeText(io: IO, x: unknown): Promise<void> {
  if (x == null) return;
  const messages = (x as { messages?: unknown }).messages;
  if (Array.isArray(messages)) {
    for (const m of messages) await write(io.stderr, `warning: ${String(m)}\n`);
  }
  await write(io.stdout, String(x));
}

/** writeBytes writes a bytes result raw. */
export async function writeBytes(io: IO, b: Uint8Array | null | undefined): Promise<void> {
  if (b != null) await write(io.stdout, b);
}

/** writeStream copies a stream result: a Node stream, a web ReadableStream, or any async iterable. */
export async function writeStream(
  io: IO,
  r: AsyncIterable<string | Uint8Array> | ReadableStream<string | Uint8Array> | null | undefined,
): Promise<void> {
  if (r == null) return;
  for await (const c of r as AsyncIterable<string | Uint8Array>) await write(io.stdout, c);
}

/** writeJSON writes any other result as one line of JSON. */
export async function writeJSON(io: IO, v: unknown): Promise<void> {
  await write(io.stdout, `${JSON.stringify(v ?? null)}\n`);
}

/** write writes one chunk, waiting for out to drain when it asks to. */
function write(out: Writable, chunk: string | Uint8Array): Promise<void> {
  return new Promise((resolve, reject) => {
    out.write(chunk, (err) => (err ? reject(err) : resolve()));
  });
}
