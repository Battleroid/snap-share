// Passphrase and password generator for the secret composer.
//
// Randomness comes from crypto.getRandomValues only (never Math.random), and
// rejection sampling keeps every choice uniform: a 32-bit draw is accepted
// only below the largest multiple of n, so `draw % n` has no bias.

import WORDS from "./eff-wordlist.js";

export const DEFAULT_WORDS = 6; // 6 × log2(7776) ≈ 77.5 bits
export const DEFAULT_LENGTH = 22; // 22 × log2(56) ≈ 127.8 bits
// Letters and digits without the look-alikes 0 O o 1 l I.
export const CHARSET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";

function fill(buf) {
  globalThis.crypto.getRandomValues(buf);
}

// randomInt returns a uniform integer in [0, n), 1 <= n <= 2^32. rand fills
// a Uint32Array (tests inject one).
export function randomInt(n, rand = fill) {
  if (!Number.isInteger(n) || n < 1 || n > 2 ** 32) throw new RangeError(`randomInt: n out of range: ${n}`);
  const limit = 2 ** 32 - (2 ** 32 % n);
  const buf = new Uint32Array(1);
  for (;;) {
    rand(buf);
    if (buf[0] < limit) return buf[0] % n;
  }
}

// passphrase returns `words` words from the EFF large wordlist.
export function passphrase(words = DEFAULT_WORDS, sep = "-", rand = fill) {
  if (!Number.isInteger(words) || words < 1 || words > 64) throw new RangeError("passphrase: 1 to 64 words");
  const out = [];
  for (let i = 0; i < words; i++) out.push(WORDS[randomInt(WORDS.length, rand)]);
  return out.join(sep);
}

// password returns `length` characters drawn from charset.
export function password(length = DEFAULT_LENGTH, charset = CHARSET, rand = fill) {
  const chars = [...charset];
  if (!Number.isInteger(length) || length < 1 || length > 256) throw new RangeError("password: 1 to 256 characters");
  if (chars.length < 2 || new Set(chars).size !== chars.length) throw new RangeError("password: charset needs 2+ distinct characters");
  let out = "";
  for (let i = 0; i < length; i++) out += chars[randomInt(chars.length, rand)];
  return out;
}

// entropyBits is the strength of a generated value.
export function entropyBits(choices, count) {
  return count * Math.log2(choices);
}

export const WORDLIST_SIZE = WORDS.length;
