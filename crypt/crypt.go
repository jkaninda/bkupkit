/*
MIT License

Copyright (c) 2026 Jonas Kaninda

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

// Package crypt builds encryptors and decryption keys from configuration.
// Encryption fails closed: if any encryption setting is present but cannot be
// used, an error is returned instead of silently writing plaintext.
package crypt

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/encryptor"
)

// Default key locations, kept from the 1.x images.
var (
	DefaultGPGPublicKeys  = []string{"/config/gnupg/public_key.asc", "/config/gnupg/public_key.gpg"}
	DefaultGPGPrivateKeys = []string{"/config/gnupg/private_key.asc", "/config/gnupg/private_key.gpg"}
	DefaultAgeIdentities  = []string{"/config/age/identity.txt", "/config/age/key.txt"}
)

// keyMaterial returns inline key content, or reads it when v is a file path.
func keyMaterial(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if strings.Contains(v, "-----BEGIN") || strings.HasPrefix(v, "AGE-SECRET-KEY-") || strings.HasPrefix(v, "age1") {
		return []byte(v), nil
	}
	b, err := os.ReadFile(v)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", v, err)
	}
	return b, nil
}

func firstExisting(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Encrypter returns the encrypter for a backup, or nil for no encryption.
func Encrypter(v *config.Values) (encryptor.Encrypter, error) {
	mode := v.String(config.Encryption)
	var opts []encryptor.Option
	if v.Bool(config.EncryptionArmor) {
		opts = append(opts, encryptor.WithArmor())
	}

	gpgPub := v.String(config.GPGPublicKey)
	if gpgPub == "" && (mode == "auto" || mode == "gpg") {
		gpgPub = firstExisting(DefaultGPGPublicKeys)
	}
	gpgPass := v.String(config.GPGPassphrase)
	ageRcpt := v.String(config.AgeRecipients)
	if f := v.String(config.AgeRecipientsFile); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", config.AgeRecipientsFile, err)
		}
		ageRcpt += "\n" + string(b)
	}
	agePass := v.String(config.AgePassphrase)
	hasGPG := gpgPub != "" || gpgPass != ""
	hasAge := strings.TrimSpace(ageRcpt) != "" || agePass != ""

	switch mode {
	case "none":
		return nil, nil
	case "auto", "":
		switch {
		case hasGPG:
			mode = "gpg" // GPG wins for compatibility with 1.x setups
		case hasAge:
			mode = "age"
		default:
			return nil, nil
		}
	}
	switch mode {
	case "gpg":
		if gpgPub != "" {
			keys, err := keyMaterial(gpgPub)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", config.GPGPublicKey, err)
			}
			e, err := encryptor.GPGRecipients([][]byte{keys}, opts...)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", config.GPGPublicKey, err)
			}
			return e, nil
		}
		if gpgPass != "" {
			return encryptor.GPGPassphrase([]byte(gpgPass), opts...)
		}
		return nil, errors.New("ENCRYPTION=gpg but neither GPG_PUBLIC_KEY nor GPG_PASSPHRASE is set")
	case "age":
		if strings.TrimSpace(ageRcpt) != "" {
			rs, err := encryptor.ParseAgeRecipients(ageRcpt)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", config.AgeRecipients, err)
			}
			e, err := encryptor.AgeRecipients(rs, opts...)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", config.AgeRecipients, err)
			}
			return e, nil
		}
		if agePass != "" {
			return encryptor.AgePassphrase([]byte(agePass), opts...)
		}
		return nil, errors.New("ENCRYPTION=age but neither AGE_RECIPIENTS(_FILE) nor AGE_PASSPHRASE is set")
	}
	return nil, fmt.Errorf("unknown encryption %q", mode)
}

// Keys collects every configured decryption key.
func Keys(v *config.Values) (encryptor.Keys, error) {
	var k encryptor.Keys
	if p := v.String(config.GPGPassphrase); p != "" {
		k.GPGPassphrases = append(k.GPGPassphrases, []byte(p))
	}
	priv := v.String(config.GPGPrivateKey)
	if priv == "" {
		priv = firstExisting(DefaultGPGPrivateKeys)
	}
	if priv != "" {
		b, err := keyMaterial(priv)
		if err != nil {
			return k, fmt.Errorf("%s: %w", config.GPGPrivateKey, err)
		}
		k.GPGPrivateKeys = append(k.GPGPrivateKeys, b)
	}
	if p := v.String(config.GPGPrivateKeyPassphrase); p != "" {
		k.GPGPrivateKeyPassphrase = []byte(p)
	}
	if id := v.String(config.AgeIdentity); id != "" {
		k.AgeIdentities = append(k.AgeIdentities, id)
	}
	idFile := v.String(config.AgeIdentityFile)
	if idFile == "" {
		idFile = firstExisting(DefaultAgeIdentities)
	}
	if idFile != "" {
		b, err := os.ReadFile(idFile)
		if err != nil {
			return k, fmt.Errorf("%s: %w", config.AgeIdentityFile, err)
		}
		k.AgeIdentities = append(k.AgeIdentities, string(b))
	}
	if p := v.String(config.AgePassphrase); p != "" {
		k.AgePassphrases = append(k.AgePassphrases, []byte(p))
	}
	return k, nil
}
