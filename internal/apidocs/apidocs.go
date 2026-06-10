// Package apidocs bettet die OpenAPI-Spezifikation ins Binary ein, sodass sie
// ohne externe Dateien ausgeliefert werden kann.
package apidocs

import _ "embed"

// Spec ist die eingebettete OpenAPI-Spezifikation (YAML).
//
//go:embed openapi.yaml
var Spec []byte
