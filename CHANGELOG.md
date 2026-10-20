# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- added pyroscope for profile
- added HLS server and connection play out for last-mile stream instead of RTSP
- added SRT-Pull for escorts
- added HLS debug logging for viewers, segments and stalls

### Changed

- optimised geo locator database load up to use less memory
- improved Database cleanup
- HLS escort redirect only keeps path and query of the request
- cleaned up logging: no more per-request, per-segment and per-frame lines, every HLS player line carries `client_ip` and `viewer_id`, consistent `node`/`component`/`module` fields, full timestamps, no colors when not writing to a terminal, `LOG_LEVEL` and `LOG_FORMAT` environment variables
- HLS redirect logs no longer include the stream passphrase

### Removed

- removed RTSP server and connection due to VR-Chat not implementing RTSP return codes

### Fixed

- fixed VR-Chat freezing on the first frame of HLS streams
- fixed SRT packets being dropped before the demuxer
- fixed HLS audio not being marked as random access
