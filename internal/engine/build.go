package engine

import (
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

// Build turns a validated claim into what the payer receives, by the
// submission rules alone (claims plan, guardrail 1: the payload is assembled
// deterministically from validated fields).
//
// Lines take the payer's code from their mapping. In grouped mode, lines
// with the same payer code become one payer line with the quantities and
// amounts added up, since those payers accept one line per item code on a
// claim (core-api-v2 submit_grouped_claim_items). A line without a payer
// code is sent with the organisation's own code only when the payer accepts
// that (unmapped_line_policy send_raw), and is never grouped. Diagnoses are
// sent primary first, each ICD-10 code once.
func Build(c *claim.Claim, cfg connector.SubmissionConfig) *connector.Submission {
	grouping := cfg.ItemGroupingOutpatient
	if c.Encounter.Type == claim.Inpatient {
		grouping = cfg.ItemGroupingInpatient
	}
	s := &connector.Submission{Claim: c}
	index := map[string]int{}
	for _, l := range c.Lines {
		code, mapped := "", l.Mapping.Mapped()
		switch {
		case mapped:
			code = l.Mapping.Target.Value
		case cfg.UnmappedLinePolicy == "send_raw" && l.Code != nil:
			code = l.Code.Value
		case cfg.UnmappedLinePolicy == "send_raw":
			code = l.ID
		default:
			continue // validation blocks the claim before it gets here
		}
		key := "line:" + l.ID
		if mapped && grouping == "grouped" {
			key = code
		}
		if i, ok := index[key]; ok {
			g := &s.Lines[i]
			g.Quantity += l.Quantity
			g.Amount += l.Total()
			g.LineIDs = append(g.LineIDs, l.ID)
			continue
		}
		description, payerName := l.Description, ""
		if mapped {
			payerName = l.Mapping.Target.Display
			if payerName != "" && key == code {
				description = payerName
			}
		}
		index[key] = len(s.Lines)
		s.Lines = append(s.Lines, connector.SendLine{
			Key: key, Code: code, Mapped: mapped, Description: description, PayerName: payerName, Category: l.Category,
			Quantity: l.Quantity, Amount: l.Total(), LineIDs: []string{l.ID},
		})
	}

	seen := map[string]bool{}
	add := func(d claim.Diagnosis) {
		icd := d.Code.Value
		if seen[icd] {
			return
		}
		seen[icd] = true
		sd := connector.SendDiagnosis{ICD: icd, Primary: d.Role == claim.Primary}
		if d.Mapping.Mapped() {
			sd.PayerCode = d.Mapping.Target.Value
		}
		s.Diagnoses = append(s.Diagnoses, sd)
	}
	for _, d := range c.Diagnoses {
		if d.Role == claim.Primary {
			add(d)
		}
	}
	for _, d := range c.Diagnoses {
		add(d)
	}
	return s
}
