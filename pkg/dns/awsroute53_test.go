package dns

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1", "2.2.2.2"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, POST

		// Verify the GET request is signed
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Equal(t, "route53.amazonaws.com", getReq.URL.Host)
		assert.True(t, strings.HasPrefix(getReq.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDTEST/"))
		assert.Contains(t, getReq.Header.Get("Authorization"), "/us-east-1/route53/aws4_request")
		assert.NotEmpty(t, getReq.Header.Get("X-Amz-Date"))

		// Verify the change request
		postReq := requests[3]
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4)

		body, err := io.ReadAll(requests[3].Body)
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4)

		// Deletions must match the existing record set exactly, including its TTL
		body, err := io.ReadAll(requests[3].Body)
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Len(t, mockTransport.GetRequests(), 3)
	})

	t.Run("No changes with existing record", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeA, 300, "9.8.7.6", "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeCNAME, awsRoute53ListBody())

		// The order is reversed from the current state, and there's a duplicate
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4", "9.8.7.6", "1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 3)
	})

	t.Run("Updates and deletes in a single change batch", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeA, 300, "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeAAAA, awsRoute53ListBody(
			awsRoute53RecordSetXML("api.example.com.", recordTypeAAAA, 300, "2001:db8::1"),
		))
		setAWSRoute53ListResponse(mockTransport, "api.example.com.", recordTypeCNAME, awsRoute53ListBody())
		setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 60, testTargets("5.6.7.8", "1.2.3.4"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "2001:db8::1"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4)

		body, err := io.ReadAll(requests[3].Body)
		require.NoError(t, err)
		changes := parseAWSRoute53Changes(t, body)
		require.Len(t, changes, 2)

		// Deletions go first in the batch
		assert.Equal(t, "DELETE", changes[0].Action)
		assert.Equal(t, recordTypeAAAA, changes[0].ResourceRecordSet.Type)
		assert.Equal(t, 300, changes[0].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"2001:db8::1"}, awsRoute53Values(changes[0].ResourceRecordSet))

		assert.Equal(t, "UPSERT", changes[1].Action)
		assert.Equal(t, recordTypeA, changes[1].ResourceRecordSet.Type)
		assert.Equal(t, 60, changes[1].ResourceRecordSet.TTL)
		assert.Equal(t, []string{"1.2.3.4", "5.6.7.8"}, awsRoute53Values(changes[1].ResourceRecordSet))
	})

	t.Run("Matches escaped wildcard names", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "*.example.com.", recordTypeA, awsRoute53ListBody(
			awsRoute53RecordSetXML(`\052.example.com.`, recordTypeA, 300, "1.2.3.4"),
		))
		setAWSRoute53ListResponse(mockTransport, "*.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "*.example.com.", recordTypeCNAME, awsRoute53ListBody())

		res, err := provider.UpdateRecords(t.Context(), "*.example.com", 300, testTargets("1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 3)
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())
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
		setAWSRoute53ListResponse(mockTransport, "www.example.com.", recordTypeCNAME, awsRoute53ListBody())

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

func TestAWSRoute53ProviderIPv6(t *testing.T) {
	provider, mockTransport := newAWSRoute53TestProviderWithMock()
	setAWSRoute53ListResponse(mockTransport, "ipv6.example.com.", recordTypeA, awsRoute53ListBody())
	setAWSRoute53ListResponse(mockTransport, "ipv6.example.com.", recordTypeAAAA, awsRoute53ListBody())
	setAWSRoute53ListResponse(mockTransport, "ipv6.example.com.", recordTypeCNAME, awsRoute53ListBody())
	setAWSRoute53ChangeResponse(mockTransport, http.StatusOK)

	_, err := provider.UpdateRecords(t.Context(), "ipv6.example.com", 60, testTargets("2001:db8::40"))
	require.NoError(t, err)

	requests := mockTransport.GetRequests()
	require.Len(t, requests, 4)

	body, err := io.ReadAll(requests[3].Body)
	require.NoError(t, err)

	changes := parseAWSRoute53Changes(t, body)
	require.Len(t, changes, 1)
	assert.Equal(t, recordTypeAAAA, changes[0].ResourceRecordSet.Type)
	assert.Equal(t, []string{"2001:db8::40"}, awsRoute53Values(changes[0].ResourceRecordSet))
}

// fakeAWSRoute53API is a stateful fake of the AWS Route53 record sets API, for the hosted zone example.com
// Like the real API, it applies the changes in a batch in order and atomically, it does not allow a CNAME record set to coexist with other record sets with the same name, and it does not allow a CNAME record set at the apex of the zone
type fakeAWSRoute53API struct {
	lock       sync.Mutex
	recordSets []awsRoute53ResourceRecordSet
	// Change batches received, each in the form "DELETE A app.example.com., UPSERT CNAME app.example.com."
	batches []string
}

const fakeAWSRoute53Apex = "example.com."

func newFakeAWSRoute53API(recordSets ...awsRoute53ResourceRecordSet) *fakeAWSRoute53API {
	return &fakeAWSRoute53API{recordSets: recordSets}
}

func (f *fakeAWSRoute53API) RoundTrip(req *http.Request) (*http.Response, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if req.URL.Path != awsRoute53TestRRSetPath {
		return fakeAWSRoute53Response(http.StatusNotFound, ""), nil
	}

	switch req.Method {
	case http.MethodGet:
		// Only exact matches are returned, which is enough for the provider
		name := req.URL.Query().Get("name")
		recordType := req.URL.Query().Get("type")
		for _, rs := range f.recordSets {
			if rs.Name == name && rs.Type == recordType {
				return fakeAWSRoute53Response(http.StatusOK, awsRoute53ListBody(awsRoute53RecordSetXML(rs.Name, rs.Type, rs.TTL, awsRoute53Values(rs)...))), nil
			}
		}
		return fakeAWSRoute53Response(http.StatusOK, awsRoute53ListBody()), nil
	case http.MethodPost:
		var changeReq awsRoute53ChangeRequest
		body, _ := io.ReadAll(req.Body)
		err := xml.Unmarshal(body, &changeReq)
		if err != nil {
			return fakeAWSRoute53Response(http.StatusBadRequest, ""), nil
		}

		ops := make([]string, len(changeReq.Changes))
		for i, c := range changeReq.Changes {
			ops[i] = c.Action + " " + c.ResourceRecordSet.Type + " " + c.ResourceRecordSet.Name
		}

		updated, err := f.apply(changeReq.Changes)
		if err != nil {
			f.batches = append(f.batches, strings.Join(ops, ", ")+" (rejected)")
			return fakeAWSRoute53Response(http.StatusBadRequest, `<?xml version="1.0"?><ErrorResponse><Error><Code>InvalidChangeBatch</Code><Message>`+err.Error()+`</Message></Error></ErrorResponse>`), nil
		}

		f.batches = append(f.batches, strings.Join(ops, ", "))
		f.recordSets = updated
		return fakeAWSRoute53Response(http.StatusOK, `<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`), nil
	default:
		return fakeAWSRoute53Response(http.StatusMethodNotAllowed, ""), nil
	}
}

// apply returns the record sets after the changes, or an error if any of them is not valid
func (f *fakeAWSRoute53API) apply(changes []awsRoute53Change) ([]awsRoute53ResourceRecordSet, error) {
	recordSets := slices.Clone(f.recordSets)
	for _, c := range changes {
		rs := c.ResourceRecordSet
		idx := slices.IndexFunc(recordSets, func(e awsRoute53ResourceRecordSet) bool {
			return e.Name == rs.Name && e.Type == rs.Type
		})

		switch c.Action {
		case "DELETE":
			// Deletions must match the existing record set exactly
			if idx < 0 || recordSets[idx].TTL != rs.TTL || !slices.Equal(awsRoute53Values(recordSets[idx]), awsRoute53Values(rs)) {
				return nil, fmt.Errorf("record set %s %s not found", rs.Type, rs.Name)
			}
			recordSets = slices.Delete(recordSets, idx, idx+1)
			continue
		case "UPSERT":
			// Handled below
		default:
			return nil, fmt.Errorf("unsupported action %s", c.Action)
		}

		if rs.Type == recordTypeCNAME && rs.Name == fakeAWSRoute53Apex {
			return nil, fmt.Errorf("CNAME record set %s is not permitted at apex", rs.Name)
		}
		for _, e := range recordSets {
			if e.Name == rs.Name && e.Type != rs.Type && (e.Type == recordTypeCNAME || rs.Type == recordTypeCNAME) {
				return nil, fmt.Errorf("record set %s %s conflicts with the existing %s record set", rs.Type, rs.Name, e.Type)
			}
		}

		if idx < 0 {
			recordSets = append(recordSets, rs)
		} else {
			recordSets[idx] = rs
		}
	}

	return recordSets, nil
}

// state returns the record sets as "TYPE name ttl=N values", sorted
func (f *fakeAWSRoute53API) state() []string {
	res := make([]string, len(f.recordSets))
	for i, rs := range f.recordSets {
		res[i] = fmt.Sprintf("%s %s ttl=%d %s", rs.Type, rs.Name, rs.TTL, strings.Join(awsRoute53Values(rs), ","))
	}
	slices.Sort(res)
	return res
}

func fakeAWSRoute53Response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"text/xml"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newAWSRoute53TestProviderWithFake(api *fakeAWSRoute53API) *AWSRoute53Provider {
	provider, _ := newAWSRoute53TestProviderWithMock()
	provider.httpClient = &http.Client{Transport: api}
	return provider
}

//nolint:maintidx
func TestAWSRoute53ProviderCNAME(t *testing.T) {
	tunnel := CNAMETarget("0a1b2c3d.cfargotunnel.com", false)

	t.Run("create a CNAME record set", func(t *testing.T) {
		api := newFakeAWSRoute53API()
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)
		assert.Equal(t, []string{"UPSERT CNAME app.example.com."}, api.batches)
		assert.Equal(t, []string{"CNAME app.example.com. ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("hostnames are compared in canonical form", func(t *testing.T) {
		api := newFakeAWSRoute53API(newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeCNAME, 120, []string{"0A1B2C3D.cfargotunnel.com."}))
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Empty(t, api.batches)
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
	})

	t.Run("A and AAAA records to a CNAME in a single change batch", func(t *testing.T) {
		api := newFakeAWSRoute53API(
			newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeA, 300, []string{"192.0.2.11", "192.0.2.10"}),
			newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeAAAA, 300, []string{"2001:db8::10"}),
		)
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"192.0.2.10", "192.0.2.11", "2001:db8::10"}, res.Previous)
		assert.Equal(t, []string{"DELETE A app.example.com., DELETE AAAA app.example.com., UPSERT CNAME app.example.com."}, api.batches)
		assert.Equal(t, []string{"CNAME app.example.com. ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("a CNAME to A and AAAA records in a single change batch", func(t *testing.T) {
		api := newFakeAWSRoute53API(newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeCNAME, 60, []string{"0a1b2c3d.cfargotunnel.com"}))
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, testTargets("192.0.2.10", "2001:db8::10"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
		assert.Equal(t, []string{"DELETE CNAME app.example.com., UPSERT A app.example.com., UPSERT AAAA app.example.com."}, api.batches)
		assert.Equal(t, []string{
			"A app.example.com. ttl=120 192.0.2.10",
			"AAAA app.example.com. ttl=120 2001:db8::10",
		}, api.state())
	})

	t.Run("changing the target of a CNAME overwrites it", func(t *testing.T) {
		api := newFakeAWSRoute53API(newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeCNAME, 120, []string{"old.cfargotunnel.com"}))
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"UPSERT CNAME app.example.com."}, api.batches)
		assert.Equal(t, []string{"CNAME app.example.com. ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("a CNAME is deleted when no targets are left", func(t *testing.T) {
		api := newFakeAWSRoute53API(newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeCNAME, 120, []string{"0a1b2c3d.cfargotunnel.com"}))
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, nil)
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"DELETE CNAME app.example.com."}, api.batches)
		assert.Empty(t, api.state())
	})

	t.Run("records for other names are not touched", func(t *testing.T) {
		api := newFakeAWSRoute53API(
			newAWSRoute53ResourceRecordSet("other.example.com.", recordTypeA, 300, []string{"192.0.2.50"}),
			newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeA, 300, []string{"192.0.2.10"}),
		)
		provider := newAWSRoute53TestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.Equal(t, []string{
			"A other.example.com. ttl=300 192.0.2.50",
			"CNAME app.example.com. ttl=120 0a1b2c3d.cfargotunnel.com",
		}, api.state())
	})

	t.Run("a rejected change batch leaves the records as they were", func(t *testing.T) {
		// AWS Route53 doesn't allow a CNAME at the apex of the zone
		api := newFakeAWSRoute53API(newAWSRoute53ResourceRecordSet("example.com.", recordTypeA, 300, []string{"192.0.2.10"}))
		provider := newAWSRoute53TestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "example.com", 120, []Target{tunnel})
		require.ErrorContains(t, err, "error updating records for domain example.com")
		require.ErrorContains(t, err, "HTTP 400")
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"DELETE A example.com., UPSERT CNAME example.com. (rejected)"}, api.batches)
		assert.Equal(t, []string{"A example.com. ttl=300 192.0.2.10"}, api.state())
	})

	t.Run("CNAME alias records are not supported", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, "app.example.com.", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "app.example.com.", recordTypeAAAA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, "app.example.com.", recordTypeCNAME, awsRoute53ListBody(
			`<ResourceRecordSet><AliasTarget><HostedZoneId>ZTEST</HostedZoneId><DNSName>other.example.com.</DNSName>`+
				`<EvaluateTargetHealth>false</EvaluateTargetHealth></AliasTarget><Name>app.example.com.</Name><Type>CNAME</Type></ResourceRecordSet>`,
		))

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.ErrorContains(t, err, "uses an alias or a routing policy other than simple")
		assert.Len(t, mockTransport.GetRequests(), 3)
	})

	t.Run("proxied targets are not supported", func(t *testing.T) {
		api := newFakeAWSRoute53API()
		provider := newAWSRoute53TestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{CNAMETarget("0a1b2c3d.cfargotunnel.com", true)})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
		assert.Empty(t, api.batches)
	})
}

func TestAWSRoute53ResourceRecordSetValues(t *testing.T) {
	t.Run("IP addresses in canonical form", func(t *testing.T) {
		values, err := newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeAAAA, 60, []string{"2001:0DB8:0:0:0:0:0:1"}).values()
		require.NoError(t, err)
		assert.Equal(t, []string{"2001:db8::1"}, values)
	})

	t.Run("hostname in canonical form", func(t *testing.T) {
		values, err := newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeCNAME, 60, []string{`Tunnel\055a.Example.NET.`}).values()
		require.NoError(t, err)
		assert.Equal(t, []string{"tunnel-a.example.net"}, values)
	})

	t.Run("invalid IP address", func(t *testing.T) {
		_, err := newAWSRoute53ResourceRecordSet("app.example.com.", recordTypeA, 60, []string{"tunnel.example.net"}).values()
		require.ErrorContains(t, err, `invalid IP address "tunnel.example.net"`)
	})

	t.Run("empty record set", func(t *testing.T) {
		values, err := awsRoute53ResourceRecordSet{}.values()
		require.NoError(t, err)
		assert.Empty(t, values)
	})
}
