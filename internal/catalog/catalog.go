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
// The catalogue is data (payer knowledge is data, CLAUDE.md). The files in
// data/ are the seed: the store copies them into the database once, operators
// edit the database copy in the console, and every process holds a snapshot
// that is swapped whole when the database copy changes (Use).
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
	"sync/atomic"

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
	// Inactive payers are kept for the claims already sent to them but
	// offered to no one.
	Inactive bool `json:"inactive,omitempty"`
	// Rules say how strictly the payer's own list is applied to claims.
	Rules PayerRules `json:"rules"`
}

// PayerRules is how a payer treats what its list does not allow. Each is
// "warn" (the claim goes, flagged; the default) or "block".
type PayerRules struct {
	// PricePolicy: a line priced above the payer's tariff.
	PricePolicy string `json:"price_policy,omitempty"`
	// UnlistedPolicy: a payer code that is not in the payer's own list.
	UnlistedPolicy string `json:"unlisted_policy,omitempty"`
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

// ListEntry is one code in a payer's own list (its tariff), as the payer or
// its platform publishes it. Operators load it so a standard item can be
// matched to the payer's code, and a mapped code checked against it.
type ListEntry struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"`
	Price *int64 `json:"price,omitempty"` // minor units
	// What the payer allows for the code, when it says: the most on one
	// claim, one sex only ("female" or "male"), an age range in whole years
	// at the visit, and prior authorisation.
	MaxQuantity        *int64 `json:"max_quantity,omitempty"`
	Sex                string `json:"sex,omitempty"`
	MinAge             *int   `json:"min_age,omitempty"`
	MaxAge             *int   `json:"max_age,omitempty"`
	NeedsAuthorization bool   `json:"needs_authorization,omitempty"`
}

// Limited reports whether the entry carries any rule beyond its price.
func (e ListEntry) Limited() bool {
	return e.MaxQuantity != nil || e.Sex != "" || e.MinAge != nil || e.MaxAge != nil || e.NeedsAuthorization
}

// Data is the catalogue as stored: what Build checks and indexes.
type Data struct {
	Payers []Payer
	Items  []Item
	Codes  map[string]map[string]PayerCode // payer -> item -> code
	Lists  map[string][]ListEntry          // payer -> its own code list
}

type Catalog struct {
	payers  []Payer
	byPayer map[string]Payer
	items   []Item
	byItem  map[string]Item
	codes   map[string]map[string]PayerCode // payer -> item -> code, generated codes included
	lists   map[string][]listed
	brands  map[string][]string       // a medicine's one-word brand or other name -> its generic, tokenised
	listed  map[string]map[string]int // payer -> code -> position in lists
	index   []indexed
}

type listed struct {
	entry   ListEntry
	words   []string
	numbers []string
}

var (
	seed = mustSeed()
	std  atomic.Pointer[Catalog]
)

func init() {
	c, err := Build(seed)
	if err != nil {
		panic("catalog: " + err.Error())
	}
	std.Store(c)
}

func get() *Catalog { return std.Load() }

// Use makes c the catalogue every lookup reads from now on.
func Use(c *Catalog) { std.Store(c) }

// Seed is the catalogue shipped in data/, which the store copies into the
// database on first start.
func Seed() Data { return seed }

func mustSeed() Data {
	d, err := readSeed(data)
	if err != nil {
		panic("catalog: " + err.Error())
	}
	return d
}

func readSeed(fsys fs.FS) (Data, error) {
	d := Data{Codes: map[string]map[string]PayerCode{}, Lists: map[string][]ListEntry{}}
	if err := readJSON(fsys, "data/payers.json", &d.Payers); err != nil {
		return d, err
	}
	if err := readJSON(fsys, "data/items.json", &d.Items); err != nil {
		return d, err
	}
	for _, p := range d.Payers {
		listed := map[string]PayerCode{}
		err := readJSON(fsys, path.Join("data/codes", p.ID+".json"), &listed)
		if err != nil && !isNotExist(err) {
			return d, err
		}
		if len(listed) > 0 {
			d.Codes[p.ID] = listed
		}
	}
	return d, nil
}

// Build checks d and indexes it for lookups.
func Build(d Data) (*Catalog, error) {
	c := &Catalog{byPayer: map[string]Payer{}, byItem: map[string]Item{}, codes: map[string]map[string]PayerCode{},
		lists: map[string][]listed{}, listed: map[string]map[string]int{}, payers: d.Payers, items: d.Items}
	for _, it := range c.items {
		if _, dup := c.byItem[it.ID]; dup || it.ID == "" || !slices.Contains(ItemKinds, it.Kind) {
			return nil, fmt.Errorf("item %q is duplicated or has no valid kind", it.ID)
		}
		c.byItem[it.ID] = it
		c.index = append(c.index, indexItem(it))
	}
	c.brands = brandsOf(c.items)
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
		for id, pc := range d.Codes[p.ID] {
			if _, ok := c.byItem[id]; !ok {
				return nil, fmt.Errorf("codes for %s name unknown item %q", p.ID, id)
			}
			codes[id] = pc
		}
		c.codes[p.ID] = codes
		at := map[string]int{}
		for _, e := range d.Lists[p.ID] {
			w := tokens(e.Name)
			at[e.Code] = len(c.lists[p.ID])
			c.lists[p.ID] = append(c.lists[p.ID], listed{entry: e, words: w, numbers: numbers(w)})
		}
		c.listed[p.ID] = at
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

// Payers lists the payers offered to organisations, by name.
func Payers() []Payer {
	var out []Payer
	for _, p := range AllPayers() {
		if !p.Inactive {
			out = append(out, p)
		}
	}
	return out
}

// AllPayers lists every payer by name, inactive ones included.
func AllPayers() []Payer {
	out := slices.Clone(get().payers)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupPayer finds a payer, inactive or not: claims already sent to one
// still need it.
func LookupPayer(id string) (Payer, bool) { p, ok := get().byPayer[id]; return p, ok }

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
	for _, it := range get().items {
		if kind == "" || it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func LookupItem(id string) (Item, bool) { it, ok := get().byItem[id]; return it, ok }

// CodeFor is the payer's code for a standard item.
func CodeFor(payerID, itemID string) (PayerCode, bool) {
	pc, ok := get().codes[payerID][itemID]
	return pc, ok
}

// CodedItems counts the standard items a payer has a code for.
func CodedItems(payerID string) int { return len(get().codes[payerID]) }

// PayerList is the payer's own code list, in the order it was loaded.
func PayerList(payerID string) []ListEntry {
	l := get().lists[payerID]
	out := make([]ListEntry, len(l))
	for i, e := range l {
		out[i] = e.entry
	}
	return out
}

// ListEntryFor finds a code in the payer's own list.
func ListEntryFor(payerID, code string) (ListEntry, bool) {
	c := get()
	i, ok := c.listed[payerID][code]
	if !ok {
		return ListEntry{}, false
	}
	return c.lists[payerID][i].entry, true
}

// HasList reports whether the payer's own list is loaded.
func HasList(payerID string) bool { return len(get().lists[payerID]) > 0 }
