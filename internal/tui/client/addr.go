package client

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseAddr interprets a --proxy value.
//
// A bare "host:port" is a proxy reachable directly over plain HTTP, which is
// what an embedded or loopback proxy is and what this flag has always meant.
//
// A URL says otherwise, and https is what a proxy behind a TLS terminator
// needs. Without it the TUI sends plain HTTP at port 443 and the reverse proxy
// answers 404 — which says nothing about what is actually wrong, since the
// request never reached zlily at all.
//
// ws and wss are accepted as synonyms of http and https, because the proxy
// address appears in both forms elsewhere and guessing wrong should not be
// punished.
func ParseAddr(spec string) (addr string, secure bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", false, fmt.Errorf("empty proxy address")
	}

	if !strings.Contains(spec, "://") {
		// host:port, as before.
		return spec, false, nil
	}

	u, err := url.Parse(spec)
	if err != nil {
		return "", false, fmt.Errorf("parse proxy address %q: %w", spec, err)
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("proxy address %q has no host", spec)
	}
	// A base path would have to be threaded through every request and the
	// WebSocket URL; refusing is better than silently ignoring it.
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", false, fmt.Errorf("proxy address %q has a path; only a host is supported", spec)
	}

	switch u.Scheme {
	case "https", "wss":
		return u.Host, true, nil
	case "http", "ws":
		return u.Host, false, nil
	}
	return "", false, fmt.Errorf("proxy address %q has scheme %q; use http:// or https://", spec, u.Scheme)
}

// Dial builds a Client for a --proxy value.
func Dial(spec string) (*Client, error) {
	addr, secure, err := ParseAddr(spec)
	if err != nil {
		return nil, err
	}
	if secure {
		return NewSecure(addr), nil
	}
	return New(addr), nil
}
