// Package catalog is Payahub's shared reference data, defined once and used
// by every organisation: the payers (insurers) and how each is reached, and
// the standard list of billable items (services, labs, imaging, procedures,
// medications, consumables) with each payer's own code for them.
//
// An organisation never recreates a payer. It picks one from the catalogue,
// and if that payer sits behind a platform (XCeli, Medserve, RX...) it adds
// its account on the platform once. It maps its own item codes to the
// standard items once; a claim line then travels
//
//	organisation code -> standard item -> the payer's code
//
// The catalogue is data in this package (payer knowledge is data, CLAUDE.md),
// reviewed like code and loaded at start.
package catalog

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

//go:embed data
var data embed.FS

// Kind of payer, from how it is reached.
const (
	Standalone = "standalone" // its own interface (a direct connector)
	Integrated = "integrated" // behind a platform shared by many payers
)

type Payer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	// Connector is the platform or direct connector that reaches the payer.
	Connector string `json:"connector"`
	// Fields are the payer's settings on its connector (its insurer code or
	// API server), the same for every organisation.
	Fields map[string]string `json:"fields,omitempty"`
	Notes  string            `json:"notes,omitempty"`
	// GeneratedCodes gives a test payer a code for every standard item:
	// prefix + "-" + item id. Real payers list their codes in data/codes.
	GeneratedCodes string `json:"generated_codes,omitempty"`
}

// Kind is standalone or integrated, from the connector's kind.
func (p Payer) Kind() string {
	if m, _, ok := connector.Lookup(p.Connector); ok && m.Kind == connector.KindDirect {
		return Standalone
	}
	return Integrated
}

// Ready reports whether claims can be sent to the payer today: its connector
// is ported and the catalogue holds every field the connector needs.
func (p Payer) Ready() bool {
	m, _, ok := connector.Lookup(p.Connector)
	return ok && m.Status == connector.StatusAvailable && connector.CheckPayerFields(m, p.Fields) == nil
}

type ItemKind string

const (
	Service    ItemKind = "service"
	Lab        ItemKind = "lab"
	Imaging    ItemKind = "imaging"
	Procedure  ItemKind = "procedure"
	Medication ItemKind = "medication"
	Consumable ItemKind = "consumable"
)

var ItemKinds = []ItemKind{Service, Lab, Imaging, Procedure, Medication, Consumable}

// System is the code system of standard items on a claim line.
const System = "payahub"

type Item struct {
	ID       string       `json:"id"`
	Kind     ItemKind     `json:"kind"`
	Name     string       `json:"name"`
	Generic  string       `json:"generic,omitempty"`
	Strength string       `json:"strength,omitempty"`
	Form     string       `json:"form,omitempty"`
	Synonyms []string     `json:"synonyms,omitempty"`
	Codes    []claim.Code `json:"codes,omitempty"` // other standards, e.g. ATC
}

// PayerCode is a payer's own code for a standard item.
type PayerCode struct {
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
}

type Catalog struct {
	payers  []Payer
	byPayer map[string]Payer
	items   []Item
	byItem  map[string]Item
	codes   map[string]map[string]PayerCode // payer -> item -> code
	index   []indexed
}

var std = mustLoad()

func mustLoad() *Catalog {
	c, err := load(data)
	if err != nil {
		panic("catalog: " + err.Error())
	}
	return c
}

func load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{byPayer: map[string]Payer{}, byItem: map[string]Item{}, codes: map[string]map[string]PayerCode{}}
	if err := readJSON(fsys, "data/payers.json", &c.payers); err != nil {
		return nil, err
	}
	if err := readJSON(fsys, "data/items.json", &c.items); err != nil {
		return nil, err
	}
	for _, it := range c.items {
		if _, dup := c.byItem[it.ID]; dup || it.ID == "" || !slices.Contains(ItemKinds, it.Kind) {
			return nil, fmt.Errorf("item %q is duplicated or has no valid kind", it.ID)
		}
		c.byItem[it.ID] = it
		c.index = append(c.index, indexItem(it))
	}
	for _, p := range c.payers {
		if _, dup := c.byPayer[p.ID]; dup || p.ID == "" || p.Connector == "" {
			return nil, fmt.Errorf("payer %q is duplicated or has no connector", p.ID)
		}
		c.byPayer[p.ID] = p
		codes := map[string]PayerCode{}
		if p.GeneratedCodes != "" {
			for _, it := range c.items {
				codes[it.ID] = PayerCode{Code: p.GeneratedCodes + "-" + it.ID, Description: it.Name}
			}
		}
		listed := map[string]PayerCode{}
		err := readJSON(fsys, path.Join("data/codes", p.ID+".json"), &listed)
		if err != nil && !isNotExist(err) {
			return nil, err
		}
		for id, pc := range listed {
			if _, ok := c.byItem[id]; !ok {
				return nil, fmt.Errorf("codes for %s name unknown item %q", p.ID, id)
			}
			codes[id] = pc
		}
		c.codes[p.ID] = codes
	}
	return c, nil
}

func readJSON(fsys fs.FS, name string, v any) error {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// Payers lists the catalogue's payers by name.
func Payers() []Payer {
	out := slices.Clone(std.payers)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func LookupPayer(id string) (Payer, bool) { p, ok := std.byPayer[id]; return p, ok }

// PayersOn lists the payers reached through a connector.
func PayersOn(connectorKey string) []Payer {
	var out []Payer
	for _, p := range Payers() {
		if p.Connector == connectorKey {
			out = append(out, p)
		}
	}
	return out
}

// Items lists standard items of a kind (all when kind is empty) in id order.
func Items(kind ItemKind) []Item {
	var out []Item
	for _, it := range std.items {
		if kind == "" || it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func LookupItem(id string) (Item, bool) { it, ok := std.byItem[id]; return it, ok }

// CodeFor is the payer's code for a standard item.
func CodeFor(payerID, itemID string) (PayerCode, bool) {
	pc, ok := std.codes[payerID][itemID]
	return pc, ok
}

// CodedItems counts the standard items a payer has a code for.
func CodedItems(payerID string) int { return len(std.codes[payerID]) }
