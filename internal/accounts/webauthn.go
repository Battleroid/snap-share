package accounts

// WebAuthn (passkeys and security keys): the relying-party configuration.
// The ceremonies, the credential store and the pages build on webAuthn().

import (
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// webAuthn returns the relying party for this site. The RP ID is the host
// of SNAP_PUBLIC_URL and the only accepted origin is the public origin:
// never the raw content origin (SNAP_RAW_URL). The display name follows
// the site.name setting, so it is built per ceremony.
//
// Registration asks for a discoverable credential and user verification
// without requiring either, so PIN-less security keys still work as a
// second step; a sign-in without a password checks user verification on
// each assertion instead. Attestation is not requested.
func (s *Service) webAuthn() (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:                  s.d.Cfg.PublicURL.Hostname(),
		RPDisplayName:         s.d.SiteName(),
		RPOrigins:             []string{s.d.Cfg.Origin()},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: challengeTTL, TimeoutUVD: challengeTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: challengeTTL, TimeoutUVD: challengeTTL},
		},
	})
}
