// Copyright 2013 @atotto. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js
// +build js

package clipboard

import "errors"

// The browser clipboard is reachable only through the asynchronous
// navigator.clipboard API, and reads from it require a user gesture and a
// permission grant. Neither fits this package's synchronous signatures, so
// js/wasm reports itself unsupported and callers fall back the same way they
// do on a Unix box with no xclip installed.

var errUnsupported = errors.New("clipboard: unsupported on js/wasm")

func init() { Unsupported = true }

func readAll() (string, error) { return "", errUnsupported }

func writeAll(string) error { return errUnsupported }
