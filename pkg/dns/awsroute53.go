package dns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

const (
	awsRoute53BaseURL = "https://route53.amazonaws.com/2013-04-01"
	// AWS Route53 is a global service, and requests are always signed for us-east-1
	awsRoute53SigningRegion = "us-east-1"
)

// AWSRoute53Provider implements the Provider interface for AWS Route53
type AWSRoute53Provider struct {
	name         string
	hostedZoneID string
	credentials  aws.CredentialsProvider
	signer       *v4.Signer
	metrics      *appmetrics.AppMetrics
	httpClient   *http.Client
}

// NewAWSRoute53Provider creates a new AWS Route53 provider
func NewAWSRoute53Provider(name string, cfg *config.AWSRoute53Config, metrics *appmetrics.AppMetrics) (*AWSRoute53Provider, error) {
	// The AWS CLI returns IDs in the "/hostedzone/<id>" format, so accept that too
	hostedZoneID := strings.TrimPrefix(cfg.HostedZoneID, "/hostedzone/")
	if hostedZoneID == "" {
		return nil, errors.New("hosted zone ID is required")
	}

	var credentials aws.CredentialsProvider
	switch {
	case cfg.AccessKeyID != "" && cfg.SecretAccessKey != "":
		// If the access key ID and secret are specified, use static credentials
		slog.Info("Authenticating to AWS with static credentials", slog.String("accessKeyId", cfg.AccessKeyID))
		credentials = awscredentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	case cfg.AccessKeyID != "" || cfg.SecretAccessKey != "":
		return nil, errors.New("access key ID and secret access key must be set together")
	default:
		// Use the default credential chain
		slog.Info("Authenticating to AWS with the default credential chain")
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("error loading default AWS configuration: %w", err)
		}

		credentials = awsCfg.Credentials
	}

	return &AWSRoute53Provider{
		name:         name,
		hostedZoneID: hostedZoneID,
		credentials:  credentials,
		signer:       v4.NewSigner(),
		metrics:      metrics,
		httpClient:   http.DefaultClient,
	}, nil
}

// Name returns the provider's name
func (r *AWSRoute53Provider) Name() string {
	return r.name
}

// Capabilities returns what the provider can publish
func (r *AWSRoute53Provider) Capabilities() Capabilities {
	return awsRoute53Capabilities
}

// UpdateRecords updates DNS A and AAAA records for the given domain with the provided IPs
// All changes are sent in a single change batch, which AWS Route53 applies atomically
func (r *AWSRoute53Provider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (result UpdateResult, err error) {
	ips, err := plainIPsFromTargets(r.name, targets)
	if err != nil {
		return UpdateResult{}, err
	}

	ipv4, ipv6, err := splitIPs(ips)
	if err != nil {
		return UpdateResult{}, err
	}

	recordName := toFQDN(domain)
	recordSets := []struct {
		recordType string
		ips        []string
	}{
		{recordType: recordTypeA, ips: ipv4},
		{recordType: recordTypeAAAA, ips: ipv6},
	}

	var changes []awsRoute53Change
	for _, rs := range recordSets {
		existing, err := r.getExistingRecordSet(ctx, recordName, rs.recordType)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error getting existing %s records: %w", rs.recordType, err)
		}

		currentIPs, err := existing.ips()
		if err != nil {
			return UpdateResult{}, fmt.Errorf("invalid IP address in existing %s record: %w", rs.recordType, err)
		}
		result.Previous = append(result.Previous, currentIPs...)

		desired := sortedUniqueIPs(rs.ips)
		switch {
		case len(desired) == 0 && len(currentIPs) == 0:
			// Nothing to do
		case len(desired) == 0:
			// Deletions must match the existing record set exactly, so we send it back as-is
			slog.DebugContext(ctx, "No healthy IPs, deleting record", slog.String("recordName", recordName), slog.String("recordType", rs.recordType))
			changes = append(changes, awsRoute53Change{
				Action:            "DELETE",
				ResourceRecordSet: existing,
			})
		case !slices.Equal(desired, sortedUniqueIPs(currentIPs)):
			slog.DebugContext(ctx, "Creating/updating record with healthy IPs", slog.String("recordName", recordName), slog.String("recordType", rs.recordType), slog.Any("ips", desired))
			changes = append(changes, awsRoute53Change{
				Action:            "UPSERT",
				ResourceRecordSet: newAWSRoute53ResourceRecordSet(recordName, rs.recordType, ttl, desired),
			})
		}
	}

	slices.Sort(result.Previous)

	if len(changes) == 0 {
		return result, nil
	}

	err = r.changeRecordSets(ctx, changes)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("error updating records for domain %s: %w", domain, err)
	}

	result.Changed = true
	return result, nil
}

// awsRoute53ResourceRecord is a single value in a record set
type awsRoute53ResourceRecord struct {
	Value string `xml:"Value"`
}

// awsRoute53ResourceRecordSet represents a record set in the AWS Route53 API
// Fields are in alphabetical order, which is the order the AWS SDK serializes them in
type awsRoute53ResourceRecordSet struct {
	// Only used to detect alias records, which are not supported
	AliasTarget     *struct{}                  `xml:"AliasTarget"`
	Name            string                     `xml:"Name"`
	ResourceRecords []awsRoute53ResourceRecord `xml:"ResourceRecords>ResourceRecord"`
	SetIdentifier   string                     `xml:"SetIdentifier,omitempty"`
	TTL             int                        `xml:"TTL,omitempty"`
	Type            string                     `xml:"Type"`
}

func newAWSRoute53ResourceRecordSet(recordName string, recordType string, ttl int, ips []string) awsRoute53ResourceRecordSet {
	records := make([]awsRoute53ResourceRecord, len(ips))
	for i, ip := range ips {
		records[i] = awsRoute53ResourceRecord{Value: ip}
	}

	return awsRoute53ResourceRecordSet{
		Name:            recordName,
		ResourceRecords: records,
		TTL:             ttl,
		Type:            recordType,
	}
}

// ips returns the canonical IPs in the record set
func (rs awsRoute53ResourceRecordSet) ips() ([]string, error) {
	ips := make([]string, len(rs.ResourceRecords))
	for i, record := range rs.ResourceRecords {
		ips[i] = record.Value
	}
	return canonicalizeIPs(ips)
}

// awsRoute53ListResponse represents the response from listing record sets
type awsRoute53ListResponse struct {
	ResourceRecordSets []awsRoute53ResourceRecordSet `xml:"ResourceRecordSets>ResourceRecordSet"`
}

// awsRoute53Change is a single change in a change batch
type awsRoute53Change struct {
	Action            string                      `xml:"Action"`
	ResourceRecordSet awsRoute53ResourceRecordSet `xml:"ResourceRecordSet"`
}

// awsRoute53ChangeRequest represents the request body for changing record sets
type awsRoute53ChangeRequest struct {
	XMLName xml.Name           `xml:"https://route53.amazonaws.com/doc/2013-04-01/ ChangeResourceRecordSetsRequest"`
	Changes []awsRoute53Change `xml:"ChangeBatch>Changes>Change"`
}

// getExistingRecordSet returns the record set with the given name and type
// If there's no such record set, it returns an empty one
func (r *AWSRoute53Provider) getExistingRecordSet(ctx context.Context, recordName string, recordType string) (awsRoute53ResourceRecordSet, error) {
	start := time.Now()
	var success bool
	if r.metrics != nil {
		defer func() {
			r.metrics.RecordAPICall("awsroute53", http.MethodGet, "/2013-04-01/hostedzone/"+r.hostedZoneID+"/rrset", success, time.Since(start))
		}()
	}

	// The list starts from the first record set whose name and type are greater than or equal to the ones requested
	// We only need the first one, then we check if it's an exact match
	params := url.Values{}
	params.Set("name", recordName)
	params.Set("type", recordType)
	params.Set("maxitems", "1")
	reqURL := awsRoute53BaseURL + "/hostedzone/" + r.hostedZoneID + "/rrset?" + params.Encode()

	var response awsRoute53ListResponse
	err := r.performRequest(ctx, http.MethodGet, reqURL, nil, &response)
	if err != nil {
		return awsRoute53ResourceRecordSet{}, err
	}
	success = true

	if len(response.ResourceRecordSets) == 0 {
		return awsRoute53ResourceRecordSet{}, nil
	}
	rs := response.ResourceRecordSets[0]
	if rs.Type != recordType || !strings.EqualFold(awsRoute53UnescapeName(rs.Name), recordName) {
		return awsRoute53ResourceRecordSet{}, nil
	}

	// ddup can only manage record sets with a simple routing policy
	if rs.AliasTarget != nil || rs.SetIdentifier != "" {
		return awsRoute53ResourceRecordSet{}, fmt.Errorf("record set %s uses an alias or a routing policy other than simple, which is not supported", recordName)
	}

	return rs, nil
}

func (r *AWSRoute53Provider) changeRecordSets(ctx context.Context, changes []awsRoute53Change) error {
	start := time.Now()
	var success bool
	if r.metrics != nil {
		defer func() {
			r.metrics.RecordAPICall("awsroute53", http.MethodPost, "/2013-04-01/hostedzone/"+r.hostedZoneID+"/rrset", success, time.Since(start))
		}()
	}

	body, err := xml.Marshal(awsRoute53ChangeRequest{Changes: changes})
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	reqURL := awsRoute53BaseURL + "/hostedzone/" + r.hostedZoneID + "/rrset"
	err = r.performRequest(ctx, http.MethodPost, reqURL, append([]byte(xml.Header), body...), nil)
	if err != nil {
		return err
	}

	success = true
	return nil
}

// performRequest sends a request signed with AWS Signature Version 4, and decodes the XML response into dest if it's not nil
func (r *AWSRoute53Provider) performRequest(ctx context.Context, method string, reqURL string, body []byte, dest any) error {
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
	}

	credentials, err := r.credentials.Retrieve(reqCtx)
	if err != nil {
		return fmt.Errorf("error retrieving AWS credentials: %w", err)
	}

	payloadHash := sha256.Sum256(body)
	err = r.signer.SignHTTP(reqCtx, credentials, req, hex.EncodeToString(payloadHash[:]), "route53", awsRoute53SigningRegion, time.Now())
	if err != nil {
		return fmt.Errorf("error signing request: %w", err)
	}

	res, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(resBody))
	}

	if dest == nil {
		return nil
	}

	err = xml.NewDecoder(res.Body).Decode(dest)
	if err != nil {
		return fmt.Errorf("error decoding response: %w", err)
	}

	return nil
}

// awsRoute53UnescapeName decodes the octal escape codes that AWS Route53 uses in names it returns
// For example, wildcard records are returned as "\052.example.com."
func awsRoute53UnescapeName(name string) string {
	if !strings.Contains(name, `\`) {
		return name
	}

	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+3 < len(name) {
			code, err := strconv.ParseUint(name[i+1:i+4], 8, 8)
			if err == nil {
				b.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// awsRoute53Capabilities declares that the provider only publishes A and AAAA records that are not proxied
var awsRoute53Capabilities = Capabilities{
	Type: "awsRoute53",
}
