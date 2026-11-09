# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- implemented minimum SRT latency for calling clients
- added `CodeQL Advanced` to create security reports automatically.
- added Dependabot version updates for Go modules, GitHub Actions and Docker images.
- added CodeQL, Go version and license badges to the README.
- added AI_DISCLOSURE to README and as a file
- added end-to-end tests (`make e2e`, build tag `e2e`) that publish a pre-encoded test clip over SRT and check the HLS output against what Windows Media Foundation, the backend of VRChat's AV-Pro player on PC, needs.
- added a Windows CI job that plays the HLS stream through Windows Media Foundation with a small .NET test player in `test/mfplayer`.

### Changed

- changed config system to now use `koanf` and be able to set defaults, distinguish between flagship, escort and shared required variables and validate variables more easily.
- updated `actions/checkout` and `actions/setup-go` to versions that run on Node.js 24.
- the changelog check no longer runs on Dependabot PRs.
- the README build badge now points to the `OverlayFox/VRC-Haven` repository.
- reworked the README
- `docker-compose.yml` now runs a flagship with `docker compose up -d` or an escort with `docker compose up -d escort`, with all settings written directly in the file instead of a `.env`, and the README setup is now Docker-first.
- moved the resource limits and pprof settings for `make performance-test` into `docker-compose.perf.yml`.

### Removed

- removed untruthful line from ReadMe
- removed unnecessary rtsp package which was left over from alpha testing

## [alpha-0.0.1] - 2026-10-22

### Added

- dynamic calculations for SRT latency
- added a pprof server on `PPROF_ADDR` (off when empty) for `go tool pprof`
- added HLS server and connection play out for last-mile stream instead of RTSP
- added SRT-Pull for escorts
- added HLS debug logging for viewers, segments and stalls
- added a dashboard on port 80 (`DASHBOARD_PORT`) with a JSON API at `/api/status`: the flagship shows its publisher, escorts with location and SRT stats, readers redirected per escort and HLS clients with their status, escorts show their upstream link and HLS clients
- added optional basic auth for the dashboard via `DASHBOARD_USER` and `DASHBOARD_PASSWORD`
- the passphrase and stream ID are set with `HAVEN_PASSPHRASE` and `HAVEN_STREAM_ID` instead of being hardcoded
- escorts read the flagship's SRT address from `FLAGSHIP_SRT_ADDR` and their HLS port from `HLS_PORT`
- added Ansible playbooks in `scripts/ansible` that deploy escorts to Linode
- escorts advertise the address they serve HLS on (`HLS_PUBLIC_ADDR`, defaults to their IP and HLS port)
- the HLS playlist survives a publisher reconnect: players keep polling the held playlist while the publisher is gone, and a publisher back within 30 seconds continues it after an `#EXT-X-DISCONTINUITY` with the same segment names and an increasing media sequence
- a publisher that does not reconnect within 30 seconds ends the HLS playlist with `#EXT-X-ENDLIST`, so players report the stream as ended instead of freezing, the ended playlist is discarded 10 seconds later
- the dashboard shows when the HLS stream is waiting for its publisher or has ended (`ended` in `/api/status`)

### Changed

- improved Database cleanup
- HLS escort redirect only keeps path and query of the request
- cleaned up logging: no more per-request, per-segment and per-frame lines, every HLS player line carries `client_ip` and `viewer_id`, consistent `node`/`component`/`module` fields, full timestamps, no colors when not writing to a terminal, `LOG_LEVEL` and `LOG_FORMAT` environment variables
- HLS redirect logs no longer include the stream passphrase
- the escort SRT stream ID is now `escort:<stream id>:<hls address>`, escorts without an HLS address are rejected
- an HLS connection now closes itself when its buffer subscription ends, so the next request starts a fresh one instead of serving a frozen playlist
- packets are no longer cloned per escort, gosrt copies the payload itself
- HLS requests while no publisher is live and no playlist is held now get a 503 with `Retry-After: 5` instead of a 500, and no longer log an error per request

### Removed

- removed RTSP server and connection due to VR-Chat not implementing RTSP return codes

### Fixed

- public ip address is now resolved from the haven when it starts
- started reading from escort to remove them from Haven once they return `io.EOF`
- fixed VR-Chat freezing on the first frame of HLS streams
- fixed VR-Chat freezing when the publisher disconnects, players got 500s for the playlist and then a new playlist whose media sequence went back to 0
- fixed SRT packets being dropped before the demuxer
- fixed HLS audio not being marked as random access
- fixed viewers being redirected to the escort's SRT source port instead of its HLS server
- fixed a data race on the escort list and a crash when looking up the closest escort without a publisher
- fixed escorts reconnecting with a different stream ID than they first connected with
- fixed every new HLS muxer starting its cleanup goroutine twice
- fixed SRT packets being dropped when gosrt delivers a burst faster than the haven reads it, the SRT reader now waits instead of dropping
- fixed the flagship disconnecting an escort when its SRT write queue is momentarily full
- fixed a crash when a publisher disconnects while frames are still being written into its buffer, the publisher's pipeline now stops before the buffer is closed
- fixed data races on the haven's publisher, demuxer and buffer fields
- fixed a data race and a crash in HLS metadata extraction when the stream ends before SPS/PPS and the audio config arrive
- fixed HLS connections hanging on close while their frame channels were full
- fixed a data race on the buffer's write position and crashes when a viewer subscribes while the buffer closes
- fixed the keyframe index growing for as long as a publisher stays connected
- fixed viewers that failed to subscribe staying in the haven and leaking their context and partial subscription
- fixed rejected SRT handshakes leaking a context
- fixed escort connections to the flagship never closing their SRT socket
- fixed the demuxer goroutine waiting forever after its input stops
- fixed the SRT server hanging on shutdown when connection requests were still queued
- fixed escorts retrying a failed connection to the flagship every 100ms forever, the delay now doubles up to 10 seconds
