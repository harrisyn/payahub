// Package all registers every connector. Import it for side effects.
package all

import (
	_ "github.com/harrisyn/payahub/internal/connector/claimiq"
	_ "github.com/harrisyn/payahub/internal/connector/medserve"
	_ "github.com/harrisyn/payahub/internal/connector/rxinsurance"
	_ "github.com/harrisyn/payahub/internal/connector/sandbox"
	_ "github.com/harrisyn/payahub/internal/connector/smartclaims"
	_ "github.com/harrisyn/payahub/internal/connector/xceli"
)
