package creds

import (
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// keyringTimeout bounds every keyring call. The backend is a D-Bus service on
// Linux, and zlily's usual home is a headless host inside GNU screen where no
// such service is running: an unanswered call there must degrade to "no
// keyring" quickly, not hold up the login dialog.
const keyringTimeout = 3 * time.Second

// errKeyringTimeout is returned when the backend does not answer in time. It is
// treated exactly like a missing keyring — the caller falls back to the file.
var errKeyringTimeout = errors.New("keyring did not respond")

// service names the keyring record. Including the Lily address keeps two
// accounts with the same handle on different servers apart, and makes the entry
// legible in Keychain Access or seahorse.
func service(host string) string { return "zlily:" + host }

// call runs a keyring operation with a deadline. On timeout the goroutine is
// left to finish on its own: it is blocked in the OS keyring client, it holds
// nothing the caller needs, and it goes away with the process.
func call[T any](fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fn()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.val, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, errKeyringTimeout
	}
}

func keyringGet(host, user string) (string, error) {
	return call(func() (string, error) { return keyring.Get(service(host), user) })
}

func keyringSet(host, user, password string) error {
	_, err := call(func() (struct{}, error) {
		return struct{}{}, keyring.Set(service(host), user, password)
	})
	if err != nil {
		return fmt.Errorf("keyring unavailable: %w", err)
	}
	return nil
}

func keyringDelete(host, user string) error {
	_, err := call(func() (struct{}, error) {
		return struct{}{}, keyring.Delete(service(host), user)
	})
	return err
}
