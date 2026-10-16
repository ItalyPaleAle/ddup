---
title: "Installation"
weight: 20
---

ddup is a single binary that runs on Linux, macOS, Windows, and FreeBSD. You can run it as a container, or download the binary and run it as a service.

Whichever way you choose, ddup needs a [configuration file](/docs/configuration).

## Container image

Container images are published on GitHub Container Registry as `ghcr.io/italypaleale/ddup`, for Linux on amd64, arm64, and armv7.

| Tag | Description |
|-----|-------------|
| `v0` | The latest 0.x release |
| `v0.6` | The latest 0.6.x release |
| `v0.6.0` | A specific release |

ddup follows [semantic versioning](https://semver.org). There is no `latest` tag: use `v0` to get updates within the 0.x releases.

The container looks for the configuration in `/etc/ddup/config.yaml`, so mount the folder that contains your `config.yaml` at `/etc/ddup`. The container runs as a non-root user (UID 65532), which must be able to read the file.

### Docker or Podman

```sh
# For Podman, replace "docker" with "podman"
docker run -d \
  --name ddup \
  --read-only \
  --restart unless-stopped \
  -v $HOME/.ddup:/etc/ddup:ro \
  ghcr.io/italypaleale/ddup:v0
```

To reach the [dashboard](/docs/dashboard), set `server.enabled: true` and `server.bind: "0.0.0.0"` in the configuration, and publish the port, for example with `-p 127.0.0.1:7401:7401`.

### Docker Compose

```yaml
services:
  ddup:
    image: "ghcr.io/italypaleale/ddup:v0"
    volumes:
      # Folder on the host that contains config.yaml
      - "/path/to/ddup:/etc/ddup:ro"
    # Uncomment to publish the dashboard, only on the host's loopback interface
    #ports:
    #  - "127.0.0.1:7401:7401"
    restart: "unless-stopped"
    read_only: true
    logging:
      driver: "json-file"
      options:
        max-file: "5"
        max-size: "20m"
```

## Pre-built binaries

Download the archive for your platform from the [releases page](https://github.com/italypaleale/ddup/releases). Archives are available for:

- Linux: amd64, arm64, armv7, and 386
- macOS: arm64 (Apple silicon) and x64 (Intel)
- Windows: x64 and arm64
- FreeBSD: amd64 and arm64

For example, on Linux amd64:

```sh
VERSION="0.6.0"
curl -fsSLO "https://github.com/italypaleale/ddup/releases/download/v${VERSION}/ddup-${VERSION}-linux-amd64.tar.gz"
tar -xzf "ddup-${VERSION}-linux-amd64.tar.gz"
sudo install "ddup-${VERSION}-linux-amd64/ddup" /usr/local/bin/ddup
```

Each release includes a `checksums.txt` file with the SHA-256 checksums of the archives, which you can use to check your download:

```sh
curl -fsSLO "https://github.com/italypaleale/ddup/releases/download/v${VERSION}/checksums.txt"
sha256sum --check --ignore-missing checksums.txt
```

Put the configuration in `/etc/ddup/config.yaml` (see [where ddup looks for the configuration](/docs/configuration#configuration-file)), then start ddup:

```sh
ddup
```

### Run as a systemd service

On Linux distributions that use systemd, run ddup as a service so it starts at boot and restarts if it stops.

1. Create a system user for ddup:

   ```sh
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin ddup
   ```

2. Make sure the configuration file is readable by that user, and not by everyone else, since it may contain credentials:

   ```sh
   sudo chown root:ddup /etc/ddup/config.yaml
   sudo chmod 640 /etc/ddup/config.yaml
   ```

3. Download the [sample unit file](https://github.com/italypaleale/ddup/blob/main/ddup.service) to `/etc/systemd/system/ddup.service`:

   ```sh
   sudo curl -fsSL -o /etc/systemd/system/ddup.service https://raw.githubusercontent.com/italypaleale/ddup/main/ddup.service
   ```

4. Start the service and enable it at boot:

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl enable --now ddup
   ```

To see the logs:

```sh
journalctl -u ddup -f
```

The sample unit runs ddup with a read-only view of the file system, and without access to home folders. If your configuration [reads secrets from other files](/docs/configuration#secrets), keep them in `/etc/ddup` or another folder outside of `/home` and `/root`.

## Building from source

Building ddup requires Go, Node.js, and pnpm, because the dashboard is embedded in the binary:

```sh
git clone https://github.com/italypaleale/ddup.git
cd ddup
make build
```

The binary is saved as `.bin/ddup`. See [DEVELOPMENT.md](https://github.com/italypaleale/ddup/blob/main/DEVELOPMENT.md) for the versions of the tools and more details.
