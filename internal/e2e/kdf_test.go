package e2e

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

var fastKDF = KDFParams{MemKiB: MinKDFMemKiB, Time: 1, Threads: 1}

func TestKDFParamsValidate(t *testing.T) {
	tests := []struct {
		name string
		p    KDFParams
		ok   bool
	}{
		{"default", DefaultKDF(), true},
		{"minimum", KDFParams{19456, 1, 1}, true},
		{"maximum", KDFParams{262144, 10, 1}, true},
		{"memory too low", KDFParams{19455, 3, 1}, false},
		{"memory too high", KDFParams{262145, 3, 1}, false},
		{"time zero", KDFParams{65536, 0, 1}, false},
		{"time too high", KDFParams{65536, 11, 1}, false},
		{"threads zero", KDFParams{65536, 3, 0}, false},
		{"threads two", KDFParams{65536, 3, 2}, false},
		{"zero value", KDFParams{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrKDFParams) {
				t.Fatalf("Validate() = %v, want ErrKDFParams", err)
			}
		})
	}
	if d := DefaultKDF(); d != (KDFParams{65536, 3, 1}) {
		t.Fatalf("DefaultKDF() = %+v", d)
	}
}

func TestHash(t *testing.T) {
	got := Hash([]byte("abc"))
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("Hash(abc) = %x", got)
	}
}

func TestInfo(t *testing.T) {
	id := [16]byte{0: 0xaa, 15: 0xbb}
	got := []byte(info("auth", id))
	want := append([]byte{4, 'a', 'u', 't', 'h'}, id[:]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("info = %x, want %x", got, want)
	}
}

func TestProbeKey(t *testing.T) {
	key, id := tb32("probe key"), tb16("probe id")
	p := ProbeKey(key, id)
	if p != ProbeKey(key, id) {
		t.Fatal("ProbeKey is not deterministic")
	}
	other := id
	other[0] ^= 1
	if p == ProbeKey(key, other) {
		t.Fatal("ProbeKey ignores the id")
	}
	k, err := DeriveKeys(key, id, [16]byte{}, nil, KDFParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range [][32]byte{k.Auth, k.Enc, k.Meta, k.Commit} {
		if x == p {
			t.Fatal("probe key equals a main key")
		}
	}
}

func TestDeriveKeys(t *testing.T) {
	key, id, salt := tb32("dk key"), tb16("dk id"), tb16("dk salt")
	base, err := DeriveKeys(key, id, salt, nil, KDFParams{})
	if err != nil {
		t.Fatal(err)
	}
	all := [][32]byte{base.Auth, base.Enc, base.Meta, base.Commit}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Fatalf("keys %d and %d are equal", i, j)
			}
		}
	}
	// Without a passphrase, the KDF parameters are ignored.
	if k, err := DeriveKeys(key, id, salt, nil, DefaultKDF()); err != nil || k != base {
		t.Fatalf("params changed keys without passphrase: %v", err)
	}

	mut := func(f func(k *[32]byte, id, salt *[16]byte)) Keys {
		k2, id2, salt2 := key, id, salt
		f(&k2, &id2, &salt2)
		k, err := DeriveKeys(k2, id2, salt2, nil, KDFParams{})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	for name, f := range map[string]func(k *[32]byte, id, salt *[16]byte){
		"key":  func(k *[32]byte, _, _ *[16]byte) { k[31] ^= 1 },
		"id":   func(_ *[32]byte, id, _ *[16]byte) { id[0] ^= 1 },
		"salt": func(_ *[32]byte, _, s *[16]byte) { s[7] ^= 1 },
	} {
		if k := mut(f); k.Commit == base.Commit || k.Enc == base.Enc {
			t.Errorf("changing the %s did not change the keys", name)
		}
	}

	withPass, err := DeriveKeys(key, id, salt, []byte("hunter2"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	if withPass.Commit == base.Commit {
		t.Fatal("passphrase did not change the keys")
	}
	other, err := DeriveKeys(key, id, salt, []byte("hunter3"), fastKDF)
	if err != nil || other.Commit == withPass.Commit {
		t.Fatalf("different passphrase gave same keys (%v)", err)
	}
	if err := other.CheckCommit(withPass.Commit[:]); !errors.Is(err, ErrCommit) {
		t.Fatalf("CheckCommit = %v", err)
	}
}

func TestDeriveKeysNFC(t *testing.T) {
	key, id, salt := tb32("nfc key"), tb16("nfc id"), tb16("nfc salt")
	nfd, err := DeriveKeys(key, id, salt, []byte("Cafe\u0301"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	nfc, err := DeriveKeys(key, id, salt, []byte("Caf\u00E9"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	if nfd != nfc {
		t.Fatal("NFD and NFC passphrases derive different keys")
	}
	nfkc, err := DeriveKeys(key, id, salt, []byte("\uFB01"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := DeriveKeys(key, id, salt, []byte("fi"), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	if nfkc == fi {
		t.Fatal("compatibility characters were folded (NFKC instead of NFC)")
	}
}

func TestDeriveKeysErrors(t *testing.T) {
	key, id, salt := tb32("err key"), tb16("err id"), tb16("err salt")
	tests := []struct {
		name string
		pass []byte
		p    KDFParams
		want error
	}{
		{"empty passphrase", []byte{}, fastKDF, ErrPassphrase},
		{"invalid utf-8", []byte{'a', 0xff}, fastKDF, ErrPassphrase},
		{"memory too low", []byte("pw"), KDFParams{19455, 1, 1}, ErrKDFParams},
		{"memory too high", []byte("pw"), KDFParams{262145, 1, 1}, ErrKDFParams},
		{"time too high", []byte("pw"), KDFParams{19456, 11, 1}, ErrKDFParams},
		{"two threads", []byte("pw"), KDFParams{19456, 1, 2}, ErrKDFParams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DeriveKeys(key, id, salt, tt.pass, tt.p); !errors.Is(err, tt.want) {
				t.Fatalf("DeriveKeys = %v, want %v", err, tt.want)
			}
			if _, err := PassphraseKey(tt.pass, salt, tt.p); !errors.Is(err, tt.want) {
				t.Fatalf("PassphraseKey = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCheckCommit(t *testing.T) {
	k := Keys{Commit: tb32("commit")}
	tests := []struct {
		name   string
		commit []byte
		ok     bool
	}{
		{"match", tb("commit", 32), true},
		{"flipped", func() []byte { b := tb("commit", 32); b[31] ^= 1; return b }(), false},
		{"short", tb("commit", 31), false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		err := k.CheckCommit(tt.commit)
		if tt.ok != (err == nil) || (!tt.ok && !errors.Is(err, ErrCommit)) {
			t.Errorf("%s: CheckCommit = %v", tt.name, err)
		}
	}
}
