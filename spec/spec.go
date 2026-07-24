// Package spec exposes the MicrosoftTeams cmdlet catalog that drives code
// generation. The catalog is derived (by reflection) from the module's own
// exported commands — see the teams-powershell-api-re factory's
// generator/extract-catalog.ps1 — and embedded here so every consumer (the Go
// binding generator cmd/gen-go, and the Terraform resource generator
// terraform-provider-teams/cmd/gen-tf) reads the same typed, versioned source of
// truth. Only this derived, factual catalog is committed — never Microsoft's
// decompiled sources.
//
// The shape mirrors go-exoscc/spec so the shared generators consume both the same
// way; the Teams-specific dispatch (which /Skype.Policy REST call a cmdlet maps
// to) is derived here (PolicyName, CRUDKind) rather than carried per row.
package spec

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed catalog/teams-catalog.json
var teamsJSON []byte

//go:embed catalog/validated-policy-types.txt
var validatedTxt string

//go:embed catalog/autorest-cmdlets.json
var autorestJSON []byte

// Catalog is the parsed cmdlet catalog.
type Catalog struct {
	Source      string   `json:"source"`
	CmdletCount int      `json:"cmdletCount"`
	Cmdlets     []Cmdlet `json:"cmdlets"`
}

// Cmdlet is a single PowerShell cmdlet (a Verb-Noun) and its parameters.
type Cmdlet struct {
	Cmdlet              string  `json:"cmdlet"`
	Verb                string  `json:"verb"`
	Noun                string  `json:"noun"`
	DefaultParameterSet string  `json:"defaultParameterSet"`
	Parameters          []Param `json:"parameters"`
}

// Param is one cmdlet parameter.
type Param struct {
	Name          string      `json:"name"`
	Type          string      `json:"type"` // string | bool | int | float | switch | stringArray | array | <.NET FQN>
	IsSwitch      bool        `json:"isSwitch"`
	ParameterSets []ParamSet  `json:"parameterSets"`
	ValidateSet   FlexStrings `json:"validateSet"`
	Aliases       FlexStrings `json:"aliases"`
}

// ParamSet is a parameter's membership in one PowerShell parameter set.
type ParamSet struct {
	Name      string `json:"name"`
	Mandatory bool   `json:"mandatory"`
	Position  *int   `json:"position"`
}

// Mandatory reports whether the parameter is mandatory in any parameter set.
func (p Param) Mandatory() bool {
	for _, s := range p.ParameterSets {
		if s.Mandatory {
			return true
		}
	}
	return false
}

// FlexStrings unmarshals either a JSON string or an array of strings —
// PowerShell's ConvertTo-Json collapses single-element arrays to a scalar.
type FlexStrings []string

func (f *FlexStrings) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var a []string
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		*f = a
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*f = FlexStrings{s}
	return nil
}

// Teams returns the MicrosoftTeams catalog.
func Teams() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(teamsJSON, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ByNoun groups the catalog's cmdlets by noun, keyed verb -> cmdlet.
func (c *Catalog) ByNoun() map[string]map[string]Cmdlet {
	out := map[string]map[string]Cmdlet{}
	for _, cm := range c.Cmdlets {
		if out[cm.Noun] == nil {
			out[cm.Noun] = map[string]Cmdlet{}
		}
		out[cm.Noun][cm.Verb] = cm
	}
	return out
}

// CRUDVerbs make a noun a full create/read/update/delete resource.
var CRUDVerbs = []string{"New", "Get", "Set", "Remove"}

// CRUDComplete returns the sorted nouns that have all four CRUD verbs and are
// therefore full Terraform-resource candidates.
func (c *Catalog) CRUDComplete() []string {
	byNoun := c.ByNoun()
	var nouns []string
	for noun, verbs := range byNoun {
		complete := true
		for _, v := range CRUDVerbs {
			if _, ok := verbs[v]; !ok {
				complete = false
				break
			}
		}
		if complete {
			nouns = append(nouns, noun)
		}
	}
	sort.Strings(nouns)
	return nouns
}

// PolicyName maps a cmdlet noun to its {PolicyName} path segment on
// /Skype.Policy/configurations. For the collection policy cmdlets this is the
// noun with the "Cs" prefix stripped (CsTeamsMeetingPolicy -> TeamsMeetingPolicy).
// A handful of singleton settings have a non-mechanical name (e.g.
// CsTenantFederationConfiguration -> TenantFederationSettings); those live in
// PolicyNameOverrides.
func PolicyName(noun string) string {
	if pn, ok := PolicyNameOverrides[noun]; ok {
		return pn
	}
	return strings.TrimPrefix(noun, "Cs")
}

// PolicyNameOverrides holds the cmdlet noun -> {PolicyName} entries that are not a
// mechanical "strip Cs" (live-validated). Extend as more are verified.
var PolicyNameOverrides = map[string]string{
	"CsTenantFederationConfiguration": "TenantFederationSettings",
}

// AutoRestRoute is a typed AutoRest cmdlet's HTTP method + path template, joined
// from the [Cmdlet] classes → autorest-catalog (decompiled).
type AutoRestRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// AutoRestRoutes returns the public-cmdlet -> {method, path} map for the typed
// AutoRest surface (non-/Skype.Policy ops).
func AutoRestRoutes() (map[string]AutoRestRoute, error) {
	var doc struct {
		Cmdlets map[string]AutoRestRoute `json:"cmdlets"`
	}
	if err := json.Unmarshal(autorestJSON, &doc); err != nil {
		return nil, err
	}
	return doc.Cmdlets, nil
}

// ValidatedPolicies is the set of {PolicyName} path segments confirmed to resolve
// (HTTP 200) against a live tenant (generator/probe-live-schema.py). The generator
// emits bindings only for nouns whose PolicyName is in this set.
func ValidatedPolicies() map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(validatedTxt, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out
}
