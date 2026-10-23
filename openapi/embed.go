// Package openapi embeds the API spec so the API can serve it.
package openapi

import _ "embed"

//go:embed payahub.v1.yaml
var Spec []byte
