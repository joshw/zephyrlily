# License Attribution

ZephyrLily is licensed under the Mozilla Public License, Version 1.1 (MPL 1.1).

## Dependencies with Different Licenses

### Hunspell English Dictionary (en_US)
**Location:** `internal/tui/ui/hunspell-en_US/`
**License:** GPL 2.0 / LGPL 2.1 / MPL 1.1 (tri-license)
**Source:** LibreOffice Dictionary Project (https://api.libreoffice.org/share/readme/LICENSE.html)
**Copyright:** Various contributors
**License Choice:** This project uses these files under the terms of the Mozilla Public License, Version 1.1

### gospell
**License:** Apache 2.0 (or compatible)
**Source:** https://github.com/client9/gospell

### Splash logo and favicon
**Location:** `internal/tui/ui/logo.txt`, `internal/webstatic/term/favicon.ico`, `internal/webstatic/term/apple-touch-icon.png`
**License:** CC BY-SA 3.0 Unported
**Source:** https://commons.wikimedia.org/wiki/File:Zephyranthes_candida.jpg
**Copyright:** Stan Shebs

The splash art and the favicon are both derived from a photograph of
*Zephyranthes candida* — the genus this project is named for — by Stan Shebs,
available under the Creative Commons Attribution-ShareAlike 3.0 Unported
licence.

The blooms in the photograph overlap, so one of them was isolated by casting
rays outward from its stamens until each met the gap between two of its own
petals. That outline was smoothed and redrawn flat: white petals with a single
mark for the stamens. The favicon is that drawing rasterised; the splash is the
same drawing as block characters, one colour per cell.

As derivatives of a share-alike work, these files are available under
CC BY-SA 3.0 rather than the MPL 1.1 that covers the rest of the project.

## Compliance Notes

The Hunspell dictionary's tri-license (GPL/LGPL/MPL) allows for use under any of the three terms. ZephyrLily has chosen to use MPL 1.1 as its license, which is the most permissive of the three and allows for:

- Proprietary/closed-source use
- Relicensing under alternative licenses (with proper attribution)
- Modification and distribution

The file-level copyleft nature of MPL 1.1 means that modifications to files containing Hunspell dictionary code must remain under MPL, but the rest of the project can be licensed differently if desired.
