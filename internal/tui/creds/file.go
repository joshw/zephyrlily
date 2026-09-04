package creds

import (
	"fmt"
	"os"
	"strings"
)

// The credentials file is line-oriented and tab-separated, in the spirit of
// tigerlily's ~/.lily/tlily/autologin: one account per line, editable by hand,
// obvious at a glance. The password is the rest of the line after the second
// tab, so anything but a newline survives a round trip.
const credentialsHeader = `# zlily saved passwords — one account per line:
#
#     <lily server>	<username>	<password>
#
# Fields are separated by tabs. Keep this file mode 0600: zlily ignores it
# entirely when anyone else can read it. Remove an entry here or with
# %forget-password.
`

// fileEntry is one account line.
type fileEntry struct {
	host     string
	user     string
	password string
}

func matches(a, b string) bool { return strings.EqualFold(a, b) }

// readFile returns the parsed entries, the raw comment/blank lines that precede
// them (so a hand-written header survives rewriting), and a warning when the
// file exists but is readable by others.
//
// A missing file is not an error: not having saved a password is the normal
// state.
func readFile() (entries []fileEntry, header []string, warn string, err error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, nil, "", err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, "", nil
	}
	if err != nil {
		return nil, nil, "", fmt.Errorf("stat %s: %w", path, err)
	}
	// A password file others can read is not a password file. Say so and skip
	// it rather than silently trusting it or silently ignoring it.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, nil, fmt.Sprintf("ignoring %s: mode %04o lets others read it (chmod 600 to use it)", path, mode), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if len(entries) == 0 && trimmed != "" {
				header = append(header, line)
			}
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			// Not a line zlily wrote. Skipping it (rather than failing) keeps
			// one fat-fingered edit from locking the user out of the rest.
			continue
		}
		entries = append(entries, fileEntry{
			host:     strings.TrimSpace(parts[0]),
			user:     strings.TrimSpace(parts[1]),
			password: parts[2],
		})
	}
	return entries, header, "", nil
}

func lookupFile(host, user string) (password, warn string, err error) {
	entries, _, warn, err := readFile()
	if err != nil {
		return "", warn, err
	}
	for _, e := range entries {
		if matches(e.host, host) && matches(e.user, user) {
			return e.password, warn, nil
		}
	}
	return "", warn, nil
}

func hasFileEntry(host, user string) (bool, error) {
	pw, _, err := lookupFile(host, user)
	return pw != "", err
}

// saveFile writes password for user on host, replacing any existing line for
// that account and leaving every other line alone.
func saveFile(host, user, password string) error {
	if strings.ContainsAny(host+user+password, "\t\n") {
		return fmt.Errorf("cannot save a password containing a tab or newline")
	}
	entries, header, warn, err := readFile()
	if err != nil {
		return err
	}
	if warn != "" {
		// Rewriting a file we refused to read would silently drop whatever is
		// in it. Make the user fix the mode first.
		return fmt.Errorf("%s", warn)
	}

	replaced := false
	for i, e := range entries {
		if matches(e.host, host) && matches(e.user, user) {
			entries[i].password = password
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, fileEntry{host: host, user: user, password: password})
	}
	return writeEntries(entries, header)
}

func forgetFile(host, user string) error {
	entries, header, warn, err := readFile()
	if err != nil || warn != "" {
		// Nothing readable to remove from; a mode warning is not a failure to
		// forget, since zlily was not using the file anyway.
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if matches(e.host, host) && matches(e.user, user) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == len(entries) {
		return nil
	}
	return writeEntries(kept, header)
}

func writeEntries(entries []fileEntry, header []string) error {
	if _, err := ensureDir(); err != nil {
		return err
	}
	path, err := credentialsPath()
	if err != nil {
		return err
	}

	var sb strings.Builder
	if len(header) > 0 {
		sb.WriteString(strings.Join(header, "\n"))
		sb.WriteString("\n")
	} else {
		sb.WriteString(credentialsHeader)
	}
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", e.host, e.user, e.password)
	}
	return writePrivate(path, []byte(sb.String()))
}
