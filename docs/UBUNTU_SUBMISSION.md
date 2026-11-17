# Ubuntu distribution plan

## 22. Debian dependency audit

The module graph contains direct dependencies (whatlanggo, redigo, collectlinks, html2text, viper,
robotstxt, modernc/sqlite) plus a large transitive tree inherited mainly from Viper 1.7.1
(grpc, oauth2, appengine, old x/net, gopkg.in/yaml.v2). Debian/Ubuntu Universe requires every
dependency to be available as a distro package and forbids downloading modules during the build.

Current state: only a subset (`golang-gopkg-yaml.v2-dev`, `golang-golang-x-net-dev`,
`golang-golang-x-sys-dev`, `golang-golang-x-text-dev`) is packaged in Debian with suitable versions.
`modernc.org/sqlite`, `whatlanggo`, `collectlinks` and `robotstxt` are not packaged as of 2026.

Decision: **PPA and Snap first** (both allow vendored/module downloads during their own build
service). Universe (item 26) remains a long-term goal that requires either packaging those modules
for Debian or replacing them with distro-available alternatives.

## 23. Snap

`snap/snapcraft.yaml` uses `confinement: strict`, `base: core24`, the Go 1.26 build snap and
`CGO_ENABLED=0`. Required interfaces: `network` (crawling), `network-bind` (optional Redis) and
`personal-files` writing to `$HOME/.local/share/gocrawler`. The crawler's default paths (`./logs`,
`./output`, `./cache`) must be overridden with `-cache=` and a working directory inside that snap
data folder; this is the main adaptation needed before publishing.

## 24. Debian / PPA

`debian/` contains a source-only skeleton (`control`, `rules`, `changelog`, `copyright`, `install`).
`debian/rules` builds with `CGO_ENABLED=0` and `-trimpath`. A Launchpad PPA can build it from the
signed source package; users then need `sudo add-apt-repository` before `apt install gocrawler`.

## 25. AppStream and desktop integration

`io.github.computerphysicslab.goCrawler.metainfo.xml` (console-application component),
`assets/io.github.computerphysicslab.goCrawler.desktop` and the SVG icon provide the metadata that
GNOME Software / App Center expects. The project license is MIT and the metadata license is CC0-1.0.

## 26. Universe / Debian Mentors (deferred)

Required steps: find a Debian sponsor, upload to Debian Mentors, pass the NEW queue with a complete
`debian/copyright` covering every source file, wait for the Debian unstable → Ubuntu Universe sync
(two-week delay plus the six-month Ubuntu cycle). Blocked by the dependency audit (item 22): the
unpackaged Go modules must be packaged first. Status: **deferred by explicit decision**, not a
technical failure.

## 27. Main / preinstalled ISO (deferred)

Requires a Main Inclusion Report, a Canonical security audit, mass adoption and a five-year LTS
support commitment. Status: **out of scope** for a hobby CLI tool; recorded here as a decision,
not as pending work.
