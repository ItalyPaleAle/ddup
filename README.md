# ddup - Dynamic DNS Health Checker

A Go application that performs periodic health checks on configured backends and updates Cloudflare DNS records based on the health status.

## Configuration

Create a `config.yaml` file with your endpoints and DNS provider settings. Multiple domains are supported and share the same DNS provider configuration:

```yaml
# Health Check and DNS Update Configuration
interval: 30s # How often to perform health checks

domains:
  - recordName: "api.example.com"
    ttl: 120
    endpoints:
      - name: "server1"
        url: "http://192.168.1.100:8080/health"
        ip: "192.168.1.100"
        timeout: 5s
      - name: "server2"
        url: "http://192.168.1.101:8080/health"
        ip: "192.168.1.101"
        timeout: 5s
  - recordName: "foo.example.com"
    ttl: 120
    endpoints:
      - name: "foo1"
        url: "https://foo1.local/health"
        ip: "10.0.0.11"
        timeout: 5s
      - name: "foo2"
        url: "https://foo2.local/health"
        ip: "10.0.0.12"
        timeout: 5s

# Provider Configuration (shared by all domains)
provider:
  cloudflare:
    zoneId: "your-zone-id"
    apiToken: "your-cloudflare-api-token"
```

### Configuration Options

#### Global Settings

- `interval`: How often to perform health checks (e.g., "30s", "1m", "5m")

#### Domains and Endpoints

- `domains`: Array of domains to manage
  - `recordName`: The DNS record to update (e.g., "api.example.com")
  - `ttl`: Time to live for DNS records
  - `endpoints`: Array of endpoints for this domain
    - `name`: Friendly name for the endpoint
    - `url`: HTTP URL to check for health status
    - `ip`: The IP address to include in DNS records when healthy
    - `timeout`: Request timeout for this specific endpoint

#### Provider Configuration (shared)

- `provider`: DNS provider name (currently only "cloudflare" supported)

#### Cloudflare Settings

- `zoneId`: Cloudflare Zone ID for your domain
- `apiToken`: Cloudflare API token with Zone:Edit permissions

## Usage

1. **Create configuration file**: Copy the example above to `config.yaml` and update with your settings

2. **Get Cloudflare credentials**:
   - API Token: Go to Cloudflare dashboard → My Profile → API Tokens → Create Token
   - Grant Zone:Edit permissions for your domain
   - Zone ID: Found in the domain overview page

3. **Build and run**:

  ```bash
   go build -o ddup .
   ./ddup
   ```

## DNS Provider Interface

The application uses a provider interface that allows easy extension to other DNS services:

```go
type Provider interface {
    UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error
}
```

To add a new provider:

1. Implement the `Provider` interface
2. Add the provider to the factory function in `internal/dns/provider.go`
3. Update the configuration structure to include provider-specific settings

## Building

```bash
# Build for current platform
make build

# Build for all platforms
make build-all
```

## License

MIT License
