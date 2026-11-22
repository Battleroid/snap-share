// Package e2e is the reference implementation of the snap one-time-secret
// envelope, version 1. The normative spec is the "E2E envelope v1" section
// of docs/security.md; internal/web/static/js/e2e.js implements the same
// envelope for browsers, and both are checked against testdata/vectors.json.
//
// A secret is addressed by a link {base}/s/{id}#{K_link}. From K_link and the
// id the client derives a probe key (proves it holds the link). From K_link,
// an optional passphrase and the envelope salt it derives the claim key
// (K_auth), the content key (K_enc), the metadata key (K_meta) and a key
// commitment tag (K_commit). Content is encrypted with STREAM over
// AES-256-GCM in 1 MiB chunks; metadata is a fixed 1 KiB record sealed with
// AES-256-GCM.
//
// Regenerate the golden vectors with:
//
//	make go ARGS="test ./internal/e2e -run TestVectors -update"
package e2e
