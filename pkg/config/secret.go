package config

import (
	"fmt"
	"os"
	"strings"

	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

// SecretString is a string config value that can be read from the environment or a file, so that secrets don't need to be written in the config file.
//
// In YAML:
//
//	apiToken: "literal value"
//	apiToken: !env CLOUDFLARE_API_TOKEN
//	apiToken: !file /run/secrets/cloudflare-token
//
// Values from `!file` have trailing newlines removed.
type SecretString string

// String implements fmt.Stringer
func (s SecretString) String() string {
	return string(s)
}

// UnmarshalYAML implements yaml.Unmarshaler
func (s *SecretString) UnmarshalYAML(node *yaml.Node) error {
	switch node.Tag {
	case "!env":
		name := strings.TrimSpace(node.Value)
		if name == "" {
			return fmt.Errorf("line %d: !env requires the name of an environment variable", node.Line)
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return fmt.Errorf("line %d: environment variable %s is not set", node.Line, name)
		}
		*s = SecretString(val)
		return nil
	case "!file":
		path := strings.TrimSpace(node.Value)
		if path == "" {
			return fmt.Errorf("line %d: !file requires a file path", node.Line)
		}
		b, err := os.ReadFile(path) //nolint:gosec // The path comes from the config file, which is trusted
		if err != nil {
			return fmt.Errorf("line %d: failed to read file for !file: %w", node.Line, err)
		}
		*s = SecretString(strings.TrimRight(string(b), "\r\n"))
		return nil
	default:
		var str string
		err := node.Decode(&str)
		if err != nil {
			return err //nolint:wrapcheck
		}
		*s = SecretString(str)
		return nil
	}
}
