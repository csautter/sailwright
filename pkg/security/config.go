// Package security reads the explicit override for unsafe guest networking.
package security

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// AllowInsecureCredentials reads only the security configuration supported today.
// A missing file keeps the protection enabled. Invalid or unknown settings fail closed.
func AllowInsecureCredentials(path string) (bool, error) {
	// #nosec G304 -- the CLI supplies its managed ConfigDir/config.yaml path; this is an intentional local configuration read.
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot read security configuration: %w", err)
	}
	defer file.Close()
	var config struct {
		Security struct {
			Allow bool `yaml:"allow_insecure_credentials"`
		} `yaml:"security"`
	}
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("invalid security configuration in %s", path)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("security configuration must contain a single YAML document")
	}
	return config.Security.Allow, nil
}
