package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/italypaleale/ddup/pkg/buildinfo"
)

const (
	// Response header with the identifier of the build, set on every /api/ response
	// The dashboard compares it with the identifier in the page, and reloads when they differ
	headerBuild = "X-Ddup-Build"

	// ID of the script element in the page with the build info
	buildScriptID = "ddup-build"
)

// buildInfo describes the build of the running app
// The values other than the version and the identifier are empty for a build that doesn't set them (a development build)
type buildInfo struct {
	// Identifier of the build, the only definition of it: it is sent in the header and in the page, and clients don't compute it
	Build     string `json:"build"`
	Version   string `json:"version"`
	BuildID   string `json:"buildId,omitempty"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"buildDate,omitempty"`
}

// newBuildInfo reads the build info set in pkg/buildinfo
func newBuildInfo() buildInfo {
	return buildInfo{
		Build:     buildIdentifier(buildinfo.AppVersion, buildinfo.BuildId, buildinfo.CommitHash),
		Version:   buildinfo.AppVersion,
		BuildID:   buildinfo.BuildId,
		Commit:    buildinfo.CommitHash,
		BuildDate: buildinfo.BuildDate,
	}
}

// buildIdentifier joins the parts that tell builds apart in one stable string
// Characters that can't be sent as-is in a header are replaced, so the header and the page always carry the same value
func buildIdentifier(version, buildID, commit string) string {
	id := strings.Join([]string{version, buildID, commit}, "|")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, id)
}

// appendHTMLSafeJSON appends a JSON document to dst, escaping the characters that could end a <script> element or break a script
// Escaping is done here instead of relying on the defaults of the JSON encoder, which differ between encoding/json and encoding/json/v2
// The characters only occur inside JSON strings, where \uXXXX is an equivalent escape
func appendHTMLSafeJSON(dst, data []byte) []byte {
	for i := 0; i < len(data); i++ {
		switch {
		case data[i] == '<':
			dst = append(dst, `\u003c`...)
		case data[i] == '>':
			dst = append(dst, `\u003e`...)
		case data[i] == '&':
			dst = append(dst, `\u0026`...)
		case bytes.HasPrefix(data[i:], []byte("\u2028")):
			dst = append(dst, `\u2028`...)
			i += len("\u2028") - 1
		case bytes.HasPrefix(data[i:], []byte("\u2029")):
			dst = append(dst, `\u2029`...)
			i += len("\u2029") - 1
		default:
			dst = append(dst, data[i])
		}
	}
	return dst
}

// insertIntoPage returns the HTML page with the build info in a non-executable JSON script element before </head>
// The JSON is escaped explicitly, so no value can end the element
func (b buildInfo) insertIntoPage(page []byte) ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the build info: %w", err)
	}

	idx := bytes.Index(page, []byte("</head>"))
	if idx < 0 {
		return nil, errors.New("the page has no </head>")
	}

	res := make([]byte, 0, len(page)+len(data)+64)
	res = append(res, page[:idx]...)
	res = append(res, `<script type="application/json" id="`+buildScriptID+`">`...)
	res = appendHTMLSafeJSON(res, data)
	res = append(res, `</script>`...)
	res = append(res, page[idx:]...)
	return res, nil
}
