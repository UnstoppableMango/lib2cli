import type { Readable } from "node:stream";

export function greet(name: string, excited: boolean): string;
export function divide(a: number, b: number): Promise<number>;
export function count(input: Readable): Promise<{ lines: number; bytes: number }>;
export function upper(input: Readable): AsyncIterable<string>;
export interface TagRequest {
  name: string;
  tags: string[];
  labels: Record<string, string>;
  note?: string;
  options?: { shout?: boolean };
}
export function tag(req: TagRequest): { label: string; note?: string };
export function version(): { toString(): string };
