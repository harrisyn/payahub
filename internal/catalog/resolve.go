package catalog

import "github.com/harrisyn/payahub/internal/claim"

// LocalCodes lists the organisation's own codes on a claim's lines that need
// a mapping looked up: lines with no payer code and no standard item yet.
func LocalCodes(c *claim.Claim) []string {
	var out []string
	for _, l := range c.Lines {
		if l.Mapping.Mapped() || l.Item != "" || l.Code == nil || l.Code.System == System {
			continue
		}
		out = append(out, l.Code.Value)
	}
	return out
}

// Resolve gives each line its standard item and the payer's code for it:
// a line already carrying a payer code is left alone; otherwise its item is
// the standard code it was sent with, or the one the organisation mapped its
// own code to (mapped: own code -> item id). Lines that cannot be resolved
// keep no target and the edit rules report them.
func Resolve(c *claim.Claim, mapped map[string]string) {
	for i := range c.Lines {
		l := &c.Lines[i]
		if l.Mapping.Mapped() {
			continue
		}
		if l.Item == "" && l.Code != nil {
			if l.Code.System == System {
				l.Item = l.Code.Value
			} else {
				l.Item = mapped[l.Code.Value]
			}
		}
		it, ok := LookupItem(l.Item)
		if !ok {
			continue
		}
		pc, ok := CodeFor(c.PayerID, it.ID)
		if !ok {
			continue
		}
		source := claim.Code{System: System, Value: it.ID, Display: it.Name}
		if l.Code != nil {
			source = *l.Code
		}
		l.Mapping = &claim.CodeMapping{
			Source: source,
			Target: &claim.Code{System: "payer:" + c.PayerID, Value: pc.Code, Display: pc.Description},
			Evidence: &claim.Evidence{
				Origin:    claim.OriginRuleDerived,
				Assertion: it.Name,
			},
		}
	}
}
