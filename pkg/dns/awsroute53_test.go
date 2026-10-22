package dns

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

const awsRoute53TestRRSetPath = "/2013-04-01/hostedzone/ZTEST/rrset"

//nolint:maintidx
func TestAWSRoute53Provider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1", "2.2.2.2"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET A, GET AAAA, POST

		// Verify the GET request is signed
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Equal(t, "route53.amazonaws.com", getReq.URL.Host)
		assert.True(t, strings.HasPrefix(getReq.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDTEST/"))
		assert.Contains(t, getReq.Header.Get("Authorization"), "/us-east-1/route53/aws4_request")
		assert.NotEmpty(t, getReq.Header.Get("X-Amz-Date"))

		// Verify the change request
		postReq := requests[2]
		assert.Equal(t, http.MethodPost, postReq.Method)
		assert.Equal(t, awsRoute53TestRRSetPath, postReq.URL.Path)
		assert.Equal(t, "application/xml", postReq.Header.Get("Content-Type"))
		assert.True(t, strings.HasPrefix(postReq.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDTEST/"))

		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">`)

		changes := parseAWSRoute53Changes(t, body)
		require.Len(t, changes, 1)
		assert.Equal(t, "UPSERT", changes[0].Action)
		assert.Equal(t, "www.example.com.", changes[0].ResourceRecordSet.Name)
		assert.Equal(t, recordTypeA, changes[0].ResourceRecordSet.Type)
		assert.Equal(t, 300, changes[0].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, awsRoute53Values(changes[0].ResourceRecordSet))
	})

	t.Run("Ignores record sets that are not an exact match", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()

		// The list continues from the next record set when there's no exact match
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("www.example.com.", "TXT", 300, `"hello"`),
		))
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody(
			awsRoute53RecordSetXML("www2.example.com.", recordTypeAAAA, 300, "2001:db8::1"),
		))
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)

		body, err := io.ReadAll(requests[2].Body)
		require.NoError(t, err)
		changes := parseAWSRoute53Changes(t, body)
		require.Len(t, changes, 1)
		assert.Equal(t, "UPSERT", changes[0].Action)
		assert.Equal(t, recordTypeA, changes[0].ResourceRecordSet.Type)
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("www.example.com.", recordTypeA, 600, "9.8.7.6", "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)

		// Deletions must match the existing record set exactly, including its TTL
		body, err := io.ReadAll(requests[2].Body)
		require.NoError(t, err)
		changes := parseAWSRoute53Changes(t, body)
		require.Len(t, changes, 1)
		assert.Equal(t, "DELETE", changes[0].Action)
		assert.Equal(t, "www.example.com.", changes[0].ResourceRecordSet.Name)
		assert.Equal(t, recordTypeA, changes[0].ResourceRecordSet.Type)
		assert.Equal(t, 600, changes[0].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"9.8.7.6", "1.2.3.4"}, awsRoute53Values(changes[0].ResourceRecordSet))
	})

	t.Run("Does not delete record if already empty", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody())

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("No changes with existing record", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeA, 300, "9.8.7.6", "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeAAAA, awsRoute53ListBody())

		// The order is reversed from the current state, and there's a duplicate
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4", "9.8.7.6", "1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("Updates and deletes in a single change batch", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeA, 300, "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeAAAA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeAAAA, 300, "2001:db8::1"),
		))
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 60, testTargets("5.6.7.8", "1.2.3.4"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "2001:db8::1"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)

		body, err := io.ReadAll(requests[2].Body)
		require.NoError(t, err)
		changes := parseAWSRoute53Changes(t, body)
		require.Len(t, changes, 2)

		assert.Equal(t, "UPSERT", changes[0].Action)
		assert.Equal(t, recordTypeA, changes[0].ResourceRecordSet.Type)
		assert.Equal(t, 60, changes[0].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"1.2.3.4", "5.6.7.8"}, awsRoute53Values(changes[0].ResourceRecordSet))

		assert.Equal(t, "DELETE", changes[1].Action)
		assert.Equal(t, recordTypeAAAA, changes[1].ResourceRecordSet.Type)
		assert.Equal(t, 300, changes[1].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"2001:db8::1"}, awsRoute53Values(changes[1].ResourceRecordSet))
	})

	t.Run("Matches escaped wildcard names", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "*.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML(`\052.example.com.`, recordTypeA, 300, "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "*.example.com.", recordTypeAAAA, awsRoute53ListBody())

		res, err := provider.UpdateRecords(t.Context(), "*.example.com", 300, testTargets("1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("Unsupported record sets", func(t *testing.T) {
		tests := []struct {
			name      string
			recordSet string
		}{
			{
				name: "alias",
				recordSet: `<ResourceRecordSet><AliasTarget><HostedZoneId>Z2FDTNDATAQYW2</HostedZoneId>` +
					`<DNSName>d111111abcdef8.cloudfront.net.</DNSName><EvaluateTargetHealth>false</EvaluateTargetHealth></AliasTarget>` +
					`<Name>www.example.com.</Name><Type>A</Type></ResourceRecordSet>`,
			},
			{
				name: "weighted",
				recordSet: `<ResourceRecordSet><Name>www.example.com.</Name><SetIdentifier>primary</SetIdentifier><TTL>300</TTL>` +
					`<Type>A</Type><Weight>10</Weight><ResourceRecords><ResourceRecord><Value>1.2.3.4</Value></ResourceRecord></ResourceRecords></ResourceRecordSet>`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				provider, mockTransport := newAWSRoute53TestProviderWithMock()
				setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody(tc.recordSet))

				_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.2.3.4"))
				require.ErrorContains(t, err, "uses an alias or a routing policy other than simple")
				assert.Len(t, mockTransport.GetRequests(), 1)
			})
		}
	})

	t.Run("Change fails", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusBadRequest)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error updating records for domain www.example.com")
		require.ErrorContains(t, err, "HTTP 400")
		assert.False(t, res.Changed)
	})

	t.Run("List fails", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error getting existing A records")
		require.ErrorContains(t, err, "HTTP 404")
		assert.Len(t, mockTransport.GetRequests(), 1)
	})

	t.Run("Invalid IP in existing record", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("www.example.com.", recordTypeA, 300, "not-an-ip"),
		))

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "invalid IP address in existing A record")
	})

	t.Run("Session token is sent", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		provider.credentials = aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "AKIDTEST", SecretAccessKey: "secret", SessionToken: "session-token"}, nil
		})
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeAAAA, awsRoute53ListBody())

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)

		requests := mockTransport.GetRequests()
		require.NotEmpty(t, requests)
		assert.Equal(t, "session-token", requests[0].Header.Get("X-Amz-Security-Token"))
	})

	t.Run("Credentials error", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		provider.credentials = aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, errors.New("no credentials")
		})

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error retrieving AWS credentials: no credentials")
		assert.Empty(t, mockTransport.GetRequests())
	})
}

func TestNewAWSRoute53Provider(t *testing.T) {
	t.Run("missing hosted zone ID", func(t *testing.T) {
		p, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{}, nil)
		require.ErrorContains(t, err, "hosted zone ID is required")
		assert.Nil(t, p)
	})

	t.Run("hosted zone ID with prefix", func(t *testing.T) {
		p, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{
			HostedZoneID:    "/hostedzone/ZTEST",
			AccessKeyID:     "AKIDTEST",
			SecretAccessKey: "secret",
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "ZTEST", p.hostedZoneID)
	})

	t.Run("static credentials", func(t *testing.T) {
		p, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{
			HostedZoneID:    "ZTEST",
			AccessKeyID:     "AKIDTEST",
			SecretAccessKey: "secret",
		}, nil)
		require.NoError(t, err)

		creds, err := p.credentials.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "AKIDTEST", creds.AccessKeyID)
		assert.Equal(t, "secret", creds.SecretAccessKey)
	})

	t.Run("access key ID without secret", func(t *testing.T) {
		_, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{HostedZoneID: "ZTEST", AccessKeyID: "AKIDTEST"}, nil)
		require.ErrorContains(t, err, "access key ID and secret access key must be set together")
	})

	t.Run("secret without access key ID", func(t *testing.T) {
		_, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{HostedZoneID: "ZTEST", SecretAccessKey: "secret"}, nil)
		require.ErrorContains(t, err, "access key ID and secret access key must be set together")
	})

	t.Run("default credential chain", func(t *testing.T) {
		// Use credentials from the environment, and ignore any config file on the machine
		dir := t.TempDir()
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
		t.Setenv("AWS_PROFILE", "")
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIDENV")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-env")
		t.Setenv("AWS_SESSION_TOKEN", "")

		p, err := NewAWSRoute53Provider("r53", &config.AWSRoute53Config{HostedZoneID: "ZTEST"}, nil)
		require.NoError(t, err)

		creds, err := p.credentials.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "AKIDENV", creds.AccessKeyID)
	})
}

func TestAWSRoute53UnescapeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "no escapes", input: "www.example.com.", expected: "www.example.com."},
		{name: "wildcard", input: `\052.example.com.`, expected: "*.example.com."},
		{name: "escape in the middle", input: `a\100b.example.com.`, expected: "a@b.example.com."},
		{name: "escape at the end", input: `a\052`, expected: "a*"},
		{name: "incomplete escape", input: `a\05`, expected: `a\05`},
		{name: "not octal", input: `a\089.example.com.`, expected: `a\089.example.com.`},
		{name: "out of range", input: `a\777.example.com.`, expected: `a\777.example.com.`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, awsRoute53UnescapeName(tc.input))
		})
	}
}

// newAWSRoute53TestProviderWithMock creates a test AWS Route53 provider with a mock HTTP client
func newAWSRoute53TestProviderWithMock() (*AWSRoute53Provider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &AWSRoute53Provider{
		name:         "test",
		hostedZoneID: "ZTEST",
		credentials: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "AKIDTEST", SecretAccessKey: "secret"}, nil
		}),
		signer:     v4.NewSigner(),
		httpClient: mockClient,
	}

	return provider, mockTransport
}

func setAWSRoute53ListResponse(mockTransport *MockHTTPTransport, recordName string, recordType string, body string) {
	params := url.Values{}
	params.Set("name", recordName)
	params.Set("type", recordType)
	params.Set("maxitems", "1")
	mockTransport.SetResponse(http.MethodGet, awsRoute53TestRRSetPath+"?"+params.Encode(), &MockResponse{
		StatusCode: http.StatusOK,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "text/xml"},
	})
}

func setAWSRoute53ChangeResponse(mockTransport *MockHTTPTransport, statusCode int) {
	body := `<?xml version="1.0"?><ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ChangeInfo><Id>/change/C1234</Id><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`
	if statusCode != http.StatusOK {
		body = `<?xml version="1.0"?><ErrorResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
			`<Error><Type>Sender</Type><Code>InvalidChangeBatch</Code><Message>boom</Message></Error></ErrorResponse>`
	}
	mockTransport.SetResponse(http.MethodPost, awsRoute53TestRRSetPath, &MockResponse{
		StatusCode: statusCode,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "text/xml"},
	})
}

func awsRoute53ListBody(recordSets ...string) string {
	return `<?xml version="1.0"?><ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<ResourceRecordSets>` + strings.Join(recordSets, "") + `</ResourceRecordSets>` +
		`<IsTruncated>false</IsTruncated><MaxItems>1</MaxItems></ListResourceRecordSetsResponse>`
}

func awsRoute53RecordSetXML(name string, recordType string, ttl int, values ...string) string {
	var b strings.Builder
	b.WriteString("<ResourceRecordSet><Name>")
	b.WriteString(name)
	b.WriteString("</Name><Type>")
	b.WriteString(recordType)
	b.WriteString("</Type><TTL>")
	b.WriteString(strconv.Itoa(ttl))
	b.WriteString("</TTL><ResourceRecords>")
	for _, v := range values {
		b.WriteString("<ResourceRecord><Value>")
		b.WriteString(v)
		b.WriteString("</Value></ResourceRecord>")
	}
	b.WriteString("</ResourceRecords></ResourceRecordSet>")
	return b.String()
}

func parseAWSRoute53Changes(t *testing.T, body []byte) []awsRoute53Change {
	t.Helper()

	var req awsRoute53ChangeRequest
	err := xml.Unmarshal(body, &req)
	require.NoError(t, err)
	return req.Changes
}

func awsRoute53Values(rs awsRoute53ResourceRecordSet) []string {
	values := make([]string, len(rs.ResourceRecords))
	for i, r := range rs.ResourceRecords {
		values[i] = r.Value
	}
	return values
}
