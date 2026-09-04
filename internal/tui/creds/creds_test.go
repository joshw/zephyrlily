package creds

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	testHost = "rpi.lily.org:7777"
	testUser = "josh"
)

// isolate points the package at a scratch config dir and a working in-memory
// keyring. Every test needs both: without the dir it would read the developer's
// real credentials file, and without the mock it would prompt the developer's
// real Keychain.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ZLILY_CONFIG_DIR", dir)
	keyring.MockInit()
	return dir
}

// noKeyring makes every keyring call fail, which is the normal state on the
// headless hosts zlily runs on.
func noKeyring(t *testing.T) {
	t.Helper()
	keyring.MockInitWithError(errors.New("no keyring on this host"))
}

func writeCredsFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "credentials")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLookupPrefersFileOverKeyring(t *testing.T) {
	dir := isolate(t)
	writeCredsFile(t, dir, testHost+"\t"+testUser+"\tfrom-file\n")
	require.NoError(t, keyring.Set(service(testHost), testUser, "from-keyring"))

	pw, where, warn := Lookup(testHost, testUser)
	require.Equal(t, "from-file", pw, "the file is the override and must win")
	require.Equal(t, LocationFile, where)
	require.Empty(t, warn)
}

func TestLookupFallsBackToKeyring(t *testing.T) {
	isolate(t)
	require.NoError(t, keyring.Set(service(testHost), testUser, "from-keyring"))

	pw, where, warn := Lookup(testHost, testUser)
	require.Equal(t, "from-keyring", pw)
	require.Equal(t, LocationKeyring, where)
	require.Empty(t, warn)
}

func TestLookupIgnoresOtherAccounts(t *testing.T) {
	dir := isolate(t)
	writeCredsFile(t, dir, strings.Join([]string{
		"other.lily.org:7777\t" + testUser + "\twrong-server",
		testHost + "\tsomeone-else\twrong-user",
	}, "\n")+"\n")

	pw, where, _ := Lookup(testHost, testUser)
	require.Empty(t, pw, "entries are keyed by server and username together")
	require.Equal(t, LocationNone, where)
}

// A file others can read is not trusted, and the user is told why rather than
// left wondering where their saved password went.
func TestLookupRefusesWorldReadableFile(t *testing.T) {
	dir := isolate(t)
	path := writeCredsFile(t, dir, testHost+"\t"+testUser+"\tfrom-file\n")
	require.NoError(t, os.Chmod(path, 0o644))

	pw, where, warn := Lookup(testHost, testUser)
	require.Empty(t, pw)
	require.Equal(t, LocationNone, where)
	require.Contains(t, warn, "0644")
	require.Contains(t, warn, "chmod 600")
}

func TestSavePrefersKeyringAndClearsTheFile(t *testing.T) {
	dir := isolate(t)
	// A password already on disk, as it would be after a save on a host with no
	// keyring — then the same account is saved again where one exists.
	writeCredsFile(t, dir, testHost+"\t"+testUser+"\tstale\n")

	where, err := Save(testHost, testUser, "current")
	require.NoError(t, err)
	require.Equal(t, LocationKeyring, where)

	stored, err := keyring.Get(service(testHost), testUser)
	require.NoError(t, err)
	require.Equal(t, "current", stored)

	// The file is read first, so a leftover line there would shadow the keyring
	// on every future login.
	onDisk, err := os.ReadFile(filepath.Join(dir, "credentials"))
	require.NoError(t, err)
	require.NotContains(t, string(onDisk), "stale")

	pw, from, _ := Lookup(testHost, testUser)
	require.Equal(t, "current", pw)
	require.Equal(t, LocationKeyring, from)
}

func TestSaveFallsBackToFileWithoutAKeyring(t *testing.T) {
	dir := isolate(t)
	noKeyring(t)

	where, err := Save(testHost, testUser, "hunter2")
	require.NoError(t, err)
	require.Equal(t, LocationFile, where)

	info, err := os.Stat(filepath.Join(dir, "credentials"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the file must not be readable by others")

	pw, from, warn := Lookup(testHost, testUser)
	require.Equal(t, "hunter2", pw)
	require.Equal(t, LocationFile, from)
	require.Empty(t, warn)
}

func TestSaveKeepsOtherAccountsAndComments(t *testing.T) {
	dir := isolate(t)
	noKeyring(t)
	writeCredsFile(t, dir, "# my own note\n"+
		"other.lily.org:7777\tjosh\tother-password\n"+
		testHost+"\t"+testUser+"\told-password\n")

	_, err := Save(testHost, testUser, "new-password")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "credentials"))
	require.NoError(t, err)
	body := string(data)
	require.Contains(t, body, "# my own note", "a hand-written header survives a rewrite")
	require.Contains(t, body, "other.lily.org:7777\tjosh\tother-password")
	require.Contains(t, body, testHost+"\t"+testUser+"\tnew-password")
	require.NotContains(t, body, "old-password", "the account is replaced, not duplicated")
}

// Rewriting a file zlily refused to read would drop whatever is in it, so the
// save fails and says what to fix.
func TestSaveRefusesToRewriteAnUnsafeFile(t *testing.T) {
	dir := isolate(t)
	noKeyring(t)
	path := writeCredsFile(t, dir, testHost+"\tsomeone\tkeep-me\n")
	require.NoError(t, os.Chmod(path, 0o644))

	_, err := Save(testHost, testUser, "hunter2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "chmod 600")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "keep-me")
}

func TestForgetClearsBothStores(t *testing.T) {
	dir := isolate(t)
	writeCredsFile(t, dir, testHost+"\t"+testUser+"\tfrom-file\n")
	require.NoError(t, keyring.Set(service(testHost), testUser, "from-keyring"))

	removed, err := Forget(testHost, testUser)
	require.NoError(t, err)
	require.ElementsMatch(t, []Location{LocationFile, LocationKeyring}, removed)

	pw, where, _ := Lookup(testHost, testUser)
	require.Empty(t, pw)
	require.Equal(t, LocationNone, where)
}

func TestForgetWithNothingStored(t *testing.T) {
	isolate(t)
	removed, err := Forget(testHost, testUser)
	require.NoError(t, err)
	require.Empty(t, removed)
}

func TestRememberUser(t *testing.T) {
	isolate(t)
	require.Empty(t, LastUser(testHost))

	require.NoError(t, RememberUser(testHost, testUser))
	require.Equal(t, testUser, LastUser(testHost))

	// A different server keeps its own answer.
	require.NoError(t, RememberUser("other.lily.org:7777", "someone-else"))
	require.Equal(t, testUser, LastUser(testHost))
	require.Equal(t, "someone-else", LastUser("other.lily.org:7777"))

	// And a later login overwrites rather than accumulating.
	require.NoError(t, RememberUser(testHost, "josh2"))
	require.Equal(t, "josh2", LastUser(testHost))
}

// The username file is a convenience, so a corrupt one must not stop a login.
func TestRememberUserSurvivesACorruptConfig(t *testing.T) {
	dir := isolate(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600))

	require.Empty(t, LastUser(testHost))
	require.NoError(t, RememberUser(testHost, testUser))
	require.Equal(t, testUser, LastUser(testHost))
}

// A password with spaces or punctuation must round-trip; only tabs and newlines
// are impossible in the file format.
func TestFileFormatRoundTrip(t *testing.T) {
	isolate(t)
	noKeyring(t)
	const password = `p a s s "w#o\rd'` + "¡"

	_, err := Save(testHost, testUser, password)
	require.NoError(t, err)
	pw, _, _ := Lookup(testHost, testUser)
	require.Equal(t, password, pw)

	_, err = Save(testHost, testUser, "with\ttab")
	require.Error(t, err)
}
