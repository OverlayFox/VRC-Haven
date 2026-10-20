# VRC-Stream-Haven

> A self-hosted CDN for VRChat streaming with intelligent geographic distribution

VRC-Stream-Haven is a distributed content delivery network designed for publishing RTSP signals to the web. <br>
It enables multiple servers to work together as a "Haven," automatically routing viewers to the geographically closest server for optimal stream stability and reduced latency.

⚠️ **Early Development Notice**: This project is currently in pre-alpha stage. Expect bugs and missing features.

## Features

- **Distributed CDN Architecture**: Multiple servers work together to serve streams efficiently
- **Intelligent Geographic Routing**: Automatically routes viewers to the nearest available server
- **SRT to RTSP Conversion**: Receives SRT feeds and remuxes to RTSP for web delivery
- **Lightweight & Efficient**: Minimal resource footprint using native Go libraries without external dependencies
- **Self-Hosted**: Full control over your streaming infrastructure

## Architecture

VRC-Stream-Haven uses a hub-and-spoke model with two types of servers:

### Flagship (Main Server)

- Receives the primary SRT feed from the broadcaster
- Manages the Haven network of Escort servers
- Routes viewers to the optimal Escort based on geographic proximity
- Serves as fallback when no Escorts are available

### Escort (Edge Server)

- Pulls the SRT feed from the Flagship
- Remuxes to RTSP for local viewers
- Registers with the Flagship via API using a shared passphrase
- Reduces load on the Flagship and improves viewer experience

**Flow Diagram:**

```
Broadcaster (SRT) → Flagship → Escort 1 → Viewers (nearby)
                            → Escort 2 → Viewers (nearby)
                            → Escort n → Viewers (nearby)
                            → Viewers (direct fallback)
```

## Prerequisites

- Go 1.26 or higher
- Network infrastructure capable of SRT/RTSP streaming
- (Optional but recommended) IP2Location LITE database for geolocation

## Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/VRC-Stream-Haven.git
cd VRC-Stream-Haven

# Build the project
make build

# Or build manually
go build -o vrc-stream-haven
```

## Configuration

### IP2Location Database Setup (Recommended)

VRC-Stream-Haven uses the [IP2Location LITE database](https://lite.ip2location.com) for IP geolocation to calculate distances between servers and viewers.

> **Privacy Note**: Escort locations are stored in RAM and logs, but viewer locations are only calculated temporarily and not persisted.

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

### Running as Flagship

```bash
./vrc-stream-haven flagship [options]
```

### Running as Escort

```bash
./vrc-stream-haven escort --flagship-url=<url> --passphrase=<secret> [options]
```

_(Full command-line documentation coming soon)_

## Roadmap

- [x] Proof of Concept
- [x] Code refactoring for improved readability and maintainability
- [x] Better circular buffering
- [x] MPEG-TS Demuxing
- [x] RTSP Muxing
- [ ] Add UPnP for easier usability by normal users
- [ ] Allow only a certain amount of viewers per node
- [ ] Syncing between SRT-Servers and RTSP clients
- [ ] Pirate Mode - allows server to only broadcast the RTSP signal on LAN
- [ ] SRT chaining - allows nodes to pull SRT streams from other nodes
- [ ] Web interface for monitoring
- [ ] When a escort disconnects the readers shouldn't be dropped but redirected to a different escort

## Contributing

Contributions are welcome! This project is in early development and could benefit from:

- Bug reports and feature requests
- Code contributions and refactoring
- Documentation improvements
- Testing and feedback

Please feel free to open issues or submit pull requests.

## License

This project is licensed under the terms specified in the [LICENSE](LICENSE) file.

## Acknowledgments

- [MaxMind](https://www.maxmind.com) for providing the geolocation database
