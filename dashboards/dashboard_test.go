// Package dashboards holds the Grafana dashboards shipped as sidecar
// ConfigMaps (dashboards/kustomization.yaml).
package dashboards

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// Flux postBuild substitution (envsubst) replaces every ${...}: Grafana
// variables must be written $name, the only ${...} is UI_URL (#13).
func TestFleetHealthSubstitution(t *testing.T) {
	data, err := os.ReadFile("fleet-health.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, m := range regexp.MustCompile(`\$\{[^}]*\}`).FindAllString(string(data), -1) {
		if m != "${UI_URL:=/archive}" {
			t.Errorf("%s would be substituted by Flux; write Grafana variables as $name", m)
		}
	}
}
