# ddup - Dynamic DNS Health Checker

A Go application that performs periodic health checks on configured backends and updates Cloudflare DNS records based on the health status.

## Features

- **Configurable Health Checks**: Define HTTP endpoints to monitor with custom timeouts
- **DNS Provider Interface**: Extensible architecture supporting multiple DNS providers (currently Cloudflare)
- **Automatic DNS Updates**: Only healthy endpoints are included in DNS records
- **YAML Configuration**: Easy-to-read configuration format
- **Concurrent Checks**: Health checks run concurrently for better performance
- **Graceful Shutdown**: Handles SIGINT and SIGTERM signals

## Configuration

Create a `config.yaml` file with your endpoints and DNS provider settings:

```yaml
# Health Check and DNS Update Configuration
interval: 30s # How often to perform health checks

# List of endpoints to health check
endpoints:
  - name: "server1"
    url: "http://192.168.1.100:8080/health"
    ip: "192.168.1.100"
    timeout: 5s
  - name: "server2"
    url: "http://192.168.1.101:8080/health"
    ip: "192.168.1.101"
    timeout: 5s
  - name: "server3"
    url: "http://192.168.1.102:8080/health"
    ip: "192.168.1.102"
    timeout: 5s

# DNS Provider Configuration
dns:
  provider: "cloudflare"
  cloudflare:
    apiToken: "your-cloudflare-api-token"
    zoneId: "your-zone-id"
    recordName: "service.example.com"
    recordType: "A"
    ttl: 300
```

### Configuration Options

#### Global Settings
- `interval`: How often to perform health checks (e.g., "30s", "1m", "5m")

#### Endpoints
- `name`: Friendly name for the endpoint
- `url`: HTTP URL to check for health status
- `ip`: The IP address to include in DNS records when healthy
- `timeout`: Request timeout for this specific endpoint

#### DNS Configuration
- `provider`: DNS provider name (currently only "cloudflare" supported)
- `recordName`: The DNS record to update (e.g., "api.example.com")
- `recordType`: DNS record type (typically "A" for IPv4)
- `ttl`: Time to live for DNS records

#### Cloudflare Settings
- `apiToken`: Cloudflare API token with Zone:Edit permissions
- `zoneId`: Cloudflare Zone ID for your domain

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

## Health Check Logic

The application:
1. Sends HTTP GET requests to each configured endpoint
2. Considers endpoints healthy if they return HTTP 2xx status codes
3. Updates DNS records to include only IPs from healthy endpoints
4. Logs all health check results and DNS updates

## DNS Provider Interface

The application uses a provider interface that allows easy extension to other DNS services:

```go
type Provider interface {
    UpdateRecords(ctx context.Context, domain string, ips []string) error
}
```

To add a new provider:
1. Implement the `Provider` interface
2. Add the provider to the factory function in `internal/dns/provider.go`
3. Update the configuration structure to include provider-specific settings

## Building

```bash
# Build for current platform
go build -o ddup .

# Build for Linux
GOOS=linux GOARCH=amd64 go build -o ddup-linux .

# Build for Windows
GOOS=windows GOARCH=amd64 go build -o ddup.exe .
```

## Dependencies

- `gopkg.in/yaml.v3`: YAML configuration parsing
- Go standard library (no external HTTP dependencies)

## License

MIT License
