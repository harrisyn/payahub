// Package all registers every connector. Import it for side effects.
package all

import (
	_ "github.com/harrisyn/payahub/internal/connector/claimiq"
	_ "github.com/harrisyn/payahub/internal/connector/sandbox"
)
