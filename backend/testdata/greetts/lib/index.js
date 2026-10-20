export function greet(name, excited) {
  return `Hello, ${name}${excited ? "!" : ""}`;
}

export async function divide(a, b) {
  if (b === 0) throw new Error("division by zero");
  return a / b;
}

export async function count(input) {
  let lines = 0;
  let bytes = 0;
  for await (const chunk of input) {
    const b = Buffer.from(chunk);
    bytes += b.length;
    lines += b.toString().split("\n").length - 1;
  }
  return { lines, bytes };
}

export async function* upper(input) {
  for await (const chunk of input) yield String(chunk).toUpperCase();
}

export function tag({ name, tags, labels, note, options }) {
  let label = [name, ...tags, ...Object.entries(labels).map(([k, v]) => `${k}=${v}`)].join(",");
  if (options?.shout) label = label.toUpperCase();
  return { label, note };
}

export function version() {
  return { toString: () => "greet v1.0" };
}
