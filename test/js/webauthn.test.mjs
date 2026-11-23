// Tests the parts of js/webauthn.js that convert between the server's JSON
// and the WebAuthn API, for browsers without parse*FromJSON and toJSON().
import { test } from "node:test";
import assert from "node:assert/strict";

import { fromB64url, toB64url, creationOptions, requestOptions, credentialJSON, message, isPath } from "../../internal/web/static/js/webauthn.js";

const bytes = (...b) => new Uint8Array(b).buffer;

test("base64url round-trips without padding", () => {
  for (const n of [0, 1, 2, 3, 4, 31, 32, 33]) {
    const b = new Uint8Array(n).map((_, i) => (i * 37 + 251) & 255);
    const s = toB64url(b.buffer);
    assert.doesNotMatch(s, /[+/=]/);
    assert.deepEqual(new Uint8Array(fromB64url(s)), b);
  }
  assert.equal(toB64url(bytes(0xfb, 0xff, 0xbf)), "-_-_");
  assert.deepEqual(new Uint8Array(fromB64url("-_-_")), new Uint8Array([0xfb, 0xff, 0xbf]));
  // A view of a larger buffer encodes only its own bytes.
  assert.equal(toB64url(new Uint8Array([9, 1, 2, 9]).subarray(1, 3)), "AQI");
});

test("creation options are decoded by hand without parseCreationOptionsFromJSON", () => {
  const json = { publicKey: {
    rp: { id: "snap.example.com", name: "snap" }, user: { id: "AQID", name: "ann", displayName: "ann" },
    challenge: "BAUG", pubKeyCredParams: [{ type: "public-key", alg: -7 }],
    excludeCredentials: [{ type: "public-key", id: "Bwg" }], extensions: { credProps: true },
  } };
  const o = creationOptions(json, {});
  assert.deepEqual(new Uint8Array(o.challenge), new Uint8Array([4, 5, 6]));
  assert.deepEqual(new Uint8Array(o.user.id), new Uint8Array([1, 2, 3]));
  assert.equal(o.user.name, "ann");
  assert.deepEqual(new Uint8Array(o.excludeCredentials[0].id), new Uint8Array([7, 8]));
  assert.equal(o.excludeCredentials[0].type, "public-key");
  assert.equal(o.rp.id, "snap.example.com");
  assert.deepEqual(o.extensions, { credProps: true });
  assert.deepEqual(creationOptions({ publicKey: { ...json.publicKey, excludeCredentials: undefined } }, {}).excludeCredentials, []);

  // The browser's own parser wins when it exists.
  const seen = [];
  const PKC = { parseCreationOptionsFromJSON: (x) => { seen.push(x); return "parsed"; } };
  assert.equal(creationOptions(json, PKC), "parsed");
  assert.equal(seen[0], json.publicKey);
});

test("request options are decoded by hand without parseRequestOptionsFromJSON", () => {
  const o = requestOptions({ publicKey: { challenge: "AAEC", rpId: "snap.example.com", userVerification: "required", allowCredentials: [{ type: "public-key", id: "AwQ", transports: ["usb"] }] } }, {});
  assert.deepEqual(new Uint8Array(o.challenge), new Uint8Array([0, 1, 2]));
  assert.deepEqual(new Uint8Array(o.allowCredentials[0].id), new Uint8Array([3, 4]));
  assert.deepEqual(o.allowCredentials[0].transports, ["usb"]);
  assert.equal(o.userVerification, "required");
  assert.deepEqual(requestOptions({ publicKey: { challenge: "AAEC" } }, {}).allowCredentials, []);
});

test("a new credential becomes toJSON() by hand", () => {
  const cred = {
    id: "AQI", rawId: bytes(1, 2), type: "public-key", authenticatorAttachment: "platform",
    response: { clientDataJSON: bytes(123, 125), attestationObject: bytes(160), getTransports: () => ["internal", "hybrid"] },
    getClientExtensionResults: () => ({ credProps: { rk: true } }),
  };
  assert.deepEqual(credentialJSON(cred), {
    id: "AQI", rawId: "AQI", type: "public-key", authenticatorAttachment: "platform",
    response: { clientDataJSON: "e30", attestationObject: "oA", transports: ["internal", "hybrid"] },
    clientExtensionResults: { credProps: { rk: true } },
  });
});

test("an assertion becomes toJSON() by hand", () => {
  const cred = {
    id: "AQI", rawId: bytes(1, 2), type: "public-key", authenticatorAttachment: null,
    response: { clientDataJSON: bytes(123, 125), authenticatorData: bytes(5), signature: bytes(6, 7), userHandle: bytes(8) },
    getClientExtensionResults: () => ({}),
  };
  assert.deepEqual(credentialJSON(cred), {
    id: "AQI", rawId: "AQI", type: "public-key",
    response: { clientDataJSON: "e30", authenticatorData: "BQ", signature: "Bgc", userHandle: "CA" },
    clientExtensionResults: {},
  });
  // No user handle (a credential that isn't discoverable).
  cred.response.userHandle = null;
  assert.equal("userHandle" in credentialJSON(cred).response, false);
  // The browser's own toJSON() wins.
  assert.equal(credentialJSON({ toJSON: () => "native" }), "native");
});

test("failures read plainly and say nothing about accounts", () => {
  const named = (name, msg) => Object.assign(new Error(msg || name), { name });
  assert.equal(message(named("AbortError")), "");
  assert.equal(message(named("NotAllowedError", "no credentials for user ann")), "The passkey request was cancelled or timed out.");
  assert.equal(message(named("InvalidStateError")), "This passkey is already added.");
  assert.equal(message(named("Failure", "That passkey didn't work.")), "That passkey didn't work.");
  assert.equal(message(named("TypeError", "x is undefined")), "Your browser couldn't use a passkey.");
  assert.equal(message(undefined), "Your browser couldn't use a passkey.");
});

test("only local paths are followed", () => {
  for (const ok of ["/", "/settings/security", "/x?y=1"]) assert.equal(isPath(ok), true, ok);
  for (const bad of ["//evil.example", "/\\evil.example", "https://evil.example/", "javascript:alert(1)", "", null, undefined]) assert.equal(isPath(bad), false, String(bad));
});
