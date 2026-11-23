// Package webauthntest is a software WebAuthn authenticator for tests. It
// makes P-256 credentials with attestation "none", builds the authenticator
// data by hand and signs like a platform authenticator would. It reads the
// options the server sends (the JSON under "publicKey") and answers with
// the JSON a browser's PublicKeyCredential.toJSON() produces, so tests can
// drive the passkey endpoints the way a browser does.
//
// Its fields set what the next ceremony reports (origin, RP ID, flags), so
// tests can also make the answers a phishing page, a raw-content page or a
// cloned key would give.
package webauthntest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Authenticator data flags (WebAuthn §6.1).
const (
	flagUP = 0x01 // user present
	flagUV = 0x04 // user verified
	flagBE = 0x08 // backup eligible
	flagBS = 0x10 // backed up
	flagAT = 0x40 // attested credential data included
)

// ErrExcluded is what Create returns when the options exclude a credential
// this authenticator holds (a browser's InvalidStateError).
var ErrExcluded = errors.New("webauthntest: a credential in excludeCredentials is on this authenticator")

// ErrNoCredential is what Get returns when no credential fits the options.
var ErrNoCredential = errors.New("webauthntest: no matching credential")

// Credential is a key pair the authenticator made.
type Credential struct {
	ID         []byte
	UserHandle []byte
	RPID       string
	Key        *ecdsa.PrivateKey
	// SignCount is the last counter value used. Get raises it first when
	// the authenticator counts; set it lower to act like a clone.
	SignCount uint32
	// BackupEligible is fixed when the credential is made; BackupState is
	// what the next assertion reports.
	BackupEligible bool
	BackupState    bool
	Discoverable   bool
}

// Authenticator holds credentials and the settings of the next ceremony.
type Authenticator struct {
	// Origin goes into the client data (the page's origin, as a browser
	// reports it).
	Origin string
	// RPID, when set, is hashed into the authenticator data instead of the
	// relying party ID the options name.
	RPID string
	// UserPresent and UserVerified are the UP and UV flags of the next
	// ceremony. BackupEligible and BackupState apply to new credentials.
	UserPresent, UserVerified   bool
	BackupEligible, BackupState bool
	// Counting raises a credential's counter on every assertion. Without
	// it the counter stays 0, as synced passkeys report.
	Counting bool
	// ResidentKey makes new credentials discoverable (a passkey); without
	// it they act like a security key that needs allowCredentials.
	ResidentKey bool
	// CrossOrigin marks the client data as from a cross-origin iframe.
	CrossOrigin bool
	// IgnoreLists makes Create skip excludeCredentials and Get skip
	// allowCredentials (a malicious client), so a test can check the
	// server's own checks. Use selects the credential Get signs with.
	IgnoreLists bool
	Use         *Credential

	Credentials []*Credential
}

// New returns an authenticator for pages on origin (e.g.
// "https://snap.example.com") that verifies its user, counts signatures and
// makes discoverable credentials.
func New(origin string) *Authenticator {
	return &Authenticator{Origin: origin, UserPresent: true, UserVerified: true, Counting: true, ResidentKey: true}
}

type descriptor struct {
	ID string `json:"id"`
}

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Exclude    []descriptor   `json:"excludeCredentials"`
		Extensions map[string]any `json:"extensions"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge string       `json:"challenge"`
		RPID      string       `json:"rpId"`
		Allow     []descriptor `json:"allowCredentials"`
	} `json:"publicKey"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// rpID is the relying party ID for options naming id: the override, the
// options' value, or the origin's host as a browser would default it.
func (a *Authenticator) rpID(id string) string {
	if a.RPID != "" {
		return a.RPID
	}
	if id != "" {
		return id
	}
	if u, err := url.Parse(a.Origin); err == nil {
		return u.Hostname()
	}
	return ""
}

func (a *Authenticator) clientData(typ string, challenge []byte) []byte {
	b, _ := json.Marshal(map[string]any{
		"type": typ, "challenge": b64(challenge), "origin": a.Origin, "crossOrigin": a.CrossOrigin,
	})
	return b
}

func (a *Authenticator) flags(c *Credential) byte {
	var f byte
	if a.UserPresent {
		f |= flagUP
	}
	if a.UserVerified {
		f |= flagUV
	}
	if c.BackupEligible {
		f |= flagBE
	}
	if c.BackupState {
		f |= flagBS
	}
	return f
}

// Create answers creation options (the server's JSON) with a new
// credential, as JSON for the finish request.
func (a *Authenticator) Create(options []byte) ([]byte, *Credential, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, nil, err
	}
	challenge, err := unb64(o.PublicKey.Challenge)
	if err != nil {
		return nil, nil, fmt.Errorf("challenge: %w", err)
	}
	user, err := unb64(o.PublicKey.User.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("user.id: %w", err)
	}
	rpid := a.rpID(o.PublicKey.RP.ID)
	if !a.IgnoreLists {
		for _, d := range o.PublicKey.Exclude {
			id, _ := unb64(d.ID)
			if a.find(id, rpid) != nil {
				return nil, nil, ErrExcluded
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	c := &Credential{
		ID: make([]byte, 32), UserHandle: user, RPID: rpid, Key: key,
		BackupEligible: a.BackupEligible, BackupState: a.BackupState, Discoverable: a.ResidentKey,
	}
	_, _ = rand.Read(c.ID)

	pub, err := key.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		return nil, nil, err
	}
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         int64(webauthncose.P256),
		XCoord:        pub[1:33],
		YCoord:        pub[33:65],
	})
	if err != nil {
		return nil, nil, err
	}
	var auth bytes.Buffer
	h := sha256.Sum256([]byte(rpid))
	auth.Write(h[:])
	auth.WriteByte(a.flags(c) | flagAT)
	_ = binary.Write(&auth, binary.BigEndian, c.SignCount)
	auth.Write(make([]byte, 16))                                 // AAGUID: none
	_ = binary.Write(&auth, binary.BigEndian, uint16(len(c.ID))) //nolint:gosec // G115: the id is 32 bytes
	auth.Write(c.ID)
	auth.Write(cose)

	att, err := webauthncbor.Marshal(struct {
		Fmt      string         `cbor:"fmt"`
		AttStmt  map[string]any `cbor:"attStmt"`
		AuthData []byte         `cbor:"authData"`
	}{"none", map[string]any{}, auth.Bytes()})
	if err != nil {
		return nil, nil, err
	}
	ext := map[string]any{}
	if v, ok := o.PublicKey.Extensions["credProps"].(bool); ok && v {
		ext["credProps"] = map[string]any{"rk": a.ResidentKey}
	}
	out, err := json.Marshal(map[string]any{
		"id": b64(c.ID), "rawId": b64(c.ID), "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{
			"clientDataJSON":    b64(a.clientData("webauthn.create", challenge)),
			"attestationObject": b64(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults": ext,
	})
	if err != nil {
		return nil, nil, err
	}
	a.Credentials = append(a.Credentials, c)
	return out, c, nil
}

func (a *Authenticator) find(id []byte, rpid string) *Credential {
	for _, c := range a.Credentials {
		if bytes.Equal(c.ID, id) && c.RPID == rpid {
			return c
		}
	}
	return nil
}

// Get answers request options (the server's JSON) with an assertion, as
// JSON for the finish request. It signs with Use when set, otherwise with
// the first credential the options allow (any discoverable one for the RP
// when they list none).
func (a *Authenticator) Get(options []byte) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	challenge, err := unb64(o.PublicKey.Challenge)
	if err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}
	rpid := a.rpID(o.PublicKey.RPID)
	c := a.Use
	if c == nil {
		c = a.pick(o, rpid)
	}
	if c == nil {
		return nil, ErrNoCredential
	}
	if a.Counting {
		c.SignCount++
	}
	var auth bytes.Buffer
	h := sha256.Sum256([]byte(rpid))
	auth.Write(h[:])
	auth.WriteByte(a.flags(c))
	_ = binary.Write(&auth, binary.BigEndian, c.SignCount)

	cdj := a.clientData("webauthn.get", challenge)
	cdh := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, auth.Bytes()...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, c.Key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64(c.ID), "rawId": b64(c.ID), "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{
			"clientDataJSON":    b64(cdj),
			"authenticatorData": b64(auth.Bytes()),
			"signature":         b64(sig),
			"userHandle":        b64(c.UserHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
}

func (a *Authenticator) pick(o requestOptions, rpid string) *Credential {
	if len(o.PublicKey.Allow) == 0 || a.IgnoreLists {
		for _, c := range a.Credentials {
			if c.RPID == rpid && c.Discoverable {
				return c
			}
		}
		return nil
	}
	for _, d := range o.PublicKey.Allow {
		id, _ := unb64(d.ID)
		if c := a.find(id, rpid); c != nil {
			return c
		}
	}
	return nil
}
