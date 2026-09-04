// Package creds remembers who logs in to which Lily server, and — when the user
// asks — their password, so the auth dialog can be answered with one keystroke
// instead of two typed fields.
//
// SLCP authenticates in-band on every connect (see docs/slcp-protocol.md), so a
// stored password has to be recoverable plaintext: there is no token to keep in
// its place and nothing useful to hash. What the two stores buy is therefore not
// secrecy from someone with the user's shell — it is keeping the password out of
// a plain dotfile on machines that offer somewhere better.
//
// There are two stores and their order is deliberately asymmetric:
//
//   - Reading checks the file first, so a line the user put there by hand is an
//     override they can see and edit.
//   - Saving prefers the keyring, so nothing lands on disk in the clear on a
//     machine that has one. A save that reaches the keyring deletes any file
//     entry for the same account, because the file is read first and a stale
//     line there would shadow the keyring forever.
//
// Everything is keyed by Lily server address and username, so the same handle on
// a test server does not collide with the real one.
package creds

import (
	"fmt"
	"strings"
)

// Location names a place a password can be kept.
type Location int

const (
	LocationNone Location = iota
	// LocationKeyring is the OS credential store: macOS Keychain, Windows
	// Credential Manager, or a freedesktop Secret Service.
	LocationKeyring
	// LocationFile is the 0600 credentials file under the zlily config dir.
	LocationFile
)

func (l Location) String() string {
	switch l {
	case LocationKeyring:
		return "keyring"
	case LocationFile:
		return "credentials file"
	}
	return "nowhere"
}

// Describe is the user-facing phrase for a save or removal, naming the file
// outright (and what its permissions do and do not promise) so nobody has to
// guess where their password went.
func (l Location) Describe() string {
	switch l {
	case LocationKeyring:
		return "your login keyring"
	case LocationFile:
		path, err := credentialsPath()
		if err != nil {
			return "the credentials file (mode 0600)"
		}
		return path + " (mode 0600 — readable by anyone who can run as you)"
	}
	return "nowhere"
}

// Lookup returns the stored password for user on host, if there is one. where
// says which store answered; warn carries anything the user should be told
// about a store that was skipped (an unsafe file mode, say) and is empty when
// there is nothing to report.
//
// A keyring that is missing or unreachable is a normal outcome here, not an
// error: the headless hosts zlily runs on usually have no Secret Service, and
// the login path must not stall on one.
func Lookup(host, user string) (password string, where Location, warn string) {
	if host == "" || user == "" {
		return "", LocationNone, ""
	}
	pw, warn, err := lookupFile(host, user)
	if err == nil && pw != "" {
		return pw, LocationFile, warn
	}
	if pw, err := keyringGet(host, user); err == nil && pw != "" {
		return pw, LocationKeyring, warn
	}
	return "", LocationNone, warn
}

// Save stores password for user on host, preferring the keyring and falling
// back to the file when the machine has no usable one. It returns where the
// password landed so the caller can say so: "saved to your login keyring" and
// "saved to a file anyone running as you can read" are different promises.
func Save(host, user, password string) (Location, error) {
	if host == "" || user == "" {
		return LocationNone, fmt.Errorf("need a server and a username to save a password")
	}
	if err := keyringSet(host, user, password); err == nil {
		// The file is read first, so a leftover line there would shadow what we
		// just stored. Drop it, and report the keyring only if it does go.
		if err := forgetFile(host, user); err != nil {
			return LocationNone, fmt.Errorf("saved to the keyring, but the old credentials-file entry could not be removed: %w", err)
		}
		return LocationKeyring, nil
	}
	if err := saveFile(host, user, password); err != nil {
		return LocationNone, err
	}
	return LocationFile, nil
}

// Forget removes any stored password for user on host from both stores and
// returns the ones it actually removed from. A user who wants a password gone
// does not care which store it was in.
func Forget(host, user string) ([]Location, error) {
	var removed []Location
	var errs []string

	had, err := hasFileEntry(host, user)
	if err != nil {
		errs = append(errs, err.Error())
	} else if had {
		if err := forgetFile(host, user); err != nil {
			errs = append(errs, err.Error())
		} else {
			removed = append(removed, LocationFile)
		}
	}

	if err := keyringDelete(host, user); err == nil {
		removed = append(removed, LocationKeyring)
	}

	if len(errs) > 0 {
		return removed, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return removed, nil
}
