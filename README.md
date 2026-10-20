<p align="center">
  <img src="./assets/gophor_caspar.png" width="250" alt="VRC Haven Logo">
</p>

<h1 align="center">VRC-Haven</h1>

<p align="center">
  <b>A CDN video streaming system build for easy deployment.</b>
</p>

<p align="center">
  <a href="https://goreportcard.com/report/github.com/OverlayFox/VRC-Haven"><img src="https://goreportcard.com/badge/github.com/OverlayFox/VRC-Haven" alt="Go Report Card"></a>
  <a href="https://github.com/OverlayFox/VRC-Haven/actions/workflows/go.yml"><img src="https://github.com/OverlayFox/VRC-Haven/actions/workflows/go.yml/badge.svg" alt="Build Status"></a>
</p>

## 1. Table of Contents

- [1. Table of Contents](#1-table-of-contents)
- [2. Introduction](#2-introduction)
- [3. Features](#3-features)
- [4. Architecture](#4-architecture)
- [5. Prerequisites](#5-prerequisites)
- [6. Installation](#6-installation)
  - [6.1. MaxMind Database Setup](#61-maxmind-database-setup)
  - [Logging](#logging)
- [Usage](#usage)
  - [7.1. Running as Flagship](#71-running-as-flagship)
  - [7.2. Running as Escort](#72-running-as-escort)
- [8. Roadmap](#8-roadmap)
- [9. Contributing](#9-contributing)
- [10. License](#10-license)
- [11. Acknowledgments](#11-acknowledgments)

## 2. Introduction

VRC-Haven is a distributed content delivery network designed for publishing HLS signals to the web. <br>
It enables multiple users to work together as a "Haven", automatically routing viewers to the geographically closest user for optimal stream stability and reduced latency.

> ⚠️ **Early Development Notice**: This project is currently in pre-alpha stage. Expect bugs and missing features.

## 3. Features

- **Distributed CDN Architecture**: Multiple users work together to serve streams efficiently
- **Intelligent Geographic Routing**: Automatically routes viewers to the nearest available server
- **SRT to HLS Conversion**: Receives SRT feeds and remuxes to HLS for web delivery
- **Lightweight & Efficient**: Minimal resource footprint using native Go libraries with minimal external software dependencies\*
- **Self-Hosted**: Full control over your streaming infrastructure

> \*:This system relies on MaxMinds GeoIP database which is free to download from MaxMinds website. See [6. Installation](#6-installation) for more information.

## 4. Architecture

VRC-Haven uses a hub-and-spoke model with two types of servers:

**Haven Overview:**

```mermaid
architecture-beta
    group flagship(server)[Flagship Sweden]
    group escort1(server)[Escort Australia]
    group escort2(server)[Escort Ukraine]

    service fingest(server)[SRT Ingest] in flagship
    service fegress(server)[HLS Egress] in flagship
    fingest:R --> L:fegress

    service e1ingest(server)[SRT Ingest] in escort1
    service e1egress(server)[HLS Egress] in escort1
    e1ingest:R --> L:e1egress
    fingest:T --> B:e1ingest

    service e2ingest(server)[SRT Ingest] in escort2
    service e2egress(server)[HLS Egress] in escort2
    e2ingest:R --> L:e2egress
    fingest:B --> T:e2ingest

    service vrc(internet)[VR Chat]
    service publisher(server)[Publisher for example OBS]

    publisher:R --> L:fingest
    fegress:R --> L:vrc
    e1egress:B --> T:vrc
    e2egress:T --> B:vrc
```

The Escorts will pull the SRT feed from the Flagship and convert them each to HLS. <br>
The flagship will then redirect the clients from inside VRC to the closest escort available or itself.

## 5. Prerequisites

- Windows or Linux PC
- Network infrastructure capable of SRT/RTSP streaming
- Router access to forward TCP/UDP Ports
- A Free MaxMind account

## 6. Installation

### 6.1. MaxMind Database Setup

VRC-Haven uses the `MaxMind GeoLite2-City Databse`for IP geolocation to calculate distances between servers and viewers.

This step is mandatory to use this application and is completely free.

**Setup Steps:**

1. Create a free account at [MaxMind](https://www.maxmind.com/en/geolite2/signup)
2. Navigate to the `Manage license keys` page
3. Click on `Generate new license key` - This is free to do
4. Give it a unique name
5. Once created, copy the `Account ID` and `License key`
6. Add your key to the config file:
   ```
   MaxMindAccountID=YOUR_ACCOUNT_ID_HERE
   MaxMindLicenseKey=YOUR_KEY_HERE
   ```

The application will automatically check for and download database updates on startup.

> **Note**: The database is not included in the repository due to licensing restrictions.

### Logging

Set these in the environment or `.env`:

- `LOG_LEVEL`: `trace`, `debug`, `info` (default), `warn` or `error`
- `LOG_FORMAT`: `json` writes one JSON object per line, anything else writes plain text (colored only on a terminal)

Lines about one HLS player share `client_ip` and `viewer_id`. To follow one player through a log file:

```bash
grep "client_ip=203.0.113.7" haven.log            # every player behind that IP, the "New HLS viewer" line gives their viewer_id
grep "viewer_id=1a2b3c4d" haven.log               # one player's session
jq 'select(.viewer_id == "1a2b3c4d")' haven.log   # the same with LOG_FORMAT=json
```

## Usage

### 7.1. Running as Flagship

```bash
./VRC-Haven flagship [options]
```

### 7.2. Running as Escort

```bash
./VRC-Haven escort --flagship-url=<url> --passphrase=<secret> [options]
```

_(Full command-line documentation coming soon)_

## 8. Roadmap

- [x] Proof of Concept
- [x] Code refactoring for improved readability and maintainability
- [x] Better circular buffering
- [x] MPEG-TS Demuxing
- [x] RTSP Muxing
- [x] Move from RTSP to HLS due to VRC not implementing RTSP return codes
- [ ] Add UPnP for easier usability by normal users
- [ ] Allow only a certain amount of viewers per node
- [ ] Syncing between SRT-Servers and HLS clients
- [ ] Pirate Mode - allows server to only broadcast the HLS signal on LAN
- [ ] SRT chaining - allows nodes to pull SRT streams from other nodes
- [ ] Web interface for monitoring
- [ ] When a escort disconnects the readers shouldn't be dropped but redirected to a different escort

## 9. Contributing

Contributions are welcome! <br>
Please feel free to open issues or submit pull requests.

## 10. License

This project is licensed under the terms specified in the [LICENSE](LICENSE) file.

## 11. Acknowledgments

- [MaxMind](https://www.maxmind.com) for providing the geolocation database
