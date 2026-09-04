//go:build js

package ui

// moshDetectable is false in the browser: there is no process table to read,
// and no mosh link to warn about.
const moshDetectable = false
