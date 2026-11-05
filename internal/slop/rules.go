// Package slop is a deterministic, offline scanner for text that carries
// recognizable machine-writing tells: em dashes, "not X, it's Y" reversals,
// recap endings, filler vocabulary, sycophancy.
//
// It flags tells. It does not estimate whether a person or a model wrote the
// text, and a hit is never evidence of authorship. Regular expressions cannot
// see structural slop (invented frameworks, restatement, uniform rhythm); that
// remains a reviewer's job.
//
// The rules are data, loaded from roster/shared/slop-rules.yaml by the caller
// and handed to ParseRules. A malformed rules file is an error here, never a
// silent fallback to a built-in list.
package slop

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Class says how a rule's hits count against a piece of text.
type Class string

const (
	// ClassHard rules fail a piece on a single hit.
	ClassHard Class = "hard"
	// ClassCounted rules are legitimate once; a piece fails when the hits of
	// all counted rules together reach the counted limit.
	ClassCounted Class = "counted"
)

// DefaultCountedLimit is used when the rules file does not set counted_limit.
const DefaultCountedLimit = 3

// Rule is one entry of the rules file, with its pattern compiled.
type Rule struct {
	ID        string
	Class     Class
	Pattern   string
	Rationale string
	Example   string
	Clean     string

	re *regexp.Regexp
}

// RuleSet is a validated, versioned list of rules.
type RuleSet struct {
	Version      int
	CountedLimit int
	Rules        []Rule
}

// Disabled records one rule switched off for a repository, with the reason.
type Disabled struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type ruleFile struct {
	Version      int        `yaml:"version"`
	CountedLimit int        `yaml:"counted_limit"`
	Rules        []ruleItem `yaml:"rules"`
}

type ruleItem struct {
	ID        string `yaml:"id"`
	Class     string `yaml:"class"`
	Pattern   string `yaml:"pattern"`
	Rationale string `yaml:"rationale"`
	Example   string `yaml:"example"`
	Clean     string `yaml:"clean"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ParseRules validates and compiles a rules file. Every problem found is
// reported together, so a broken file is fixed in one pass.
func ParseRules(data []byte) (*RuleSet, error) {
	var file ruleFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("slop rules: %w", err)
	}

	var problems []string
	if file.Version < 1 {
		problems = append(problems, "version must be a positive integer")
	}
	limit := file.CountedLimit
	if limit == 0 {
		limit = DefaultCountedLimit
	}
	if limit < 1 {
		problems = append(problems, "counted_limit must be a positive integer")
	}
	if len(file.Rules) == 0 {
		problems = append(problems, "rules is empty; a scanner with no rules would report every text as clear")
	}

	seen := map[string]bool{}
	set := &RuleSet{Version: file.Version, CountedLimit: limit}
	for index, item := range file.Rules {
		label := fmt.Sprintf("rules[%d]", index)
		if item.ID != "" {
			label += " (" + item.ID + ")"
		}
		if !idPattern.MatchString(item.ID) {
			problems = append(problems, label+": id must be lowercase kebab-case")
		} else if seen[item.ID] {
			problems = append(problems, label+": duplicate id")
		}
		seen[item.ID] = true

		class := Class(item.Class)
		if class != ClassHard && class != ClassCounted {
			problems = append(problems, label+": class must be \"hard\" or \"counted\"")
		}
		for field, value := range map[string]string{
			"pattern": item.Pattern, "rationale": item.Rationale,
			"example": item.Example, "clean": item.Clean,
		} {
			if strings.TrimSpace(value) == "" {
				problems = append(problems, label+": "+field+" is required")
			}
		}
		compiled, err := regexp.Compile(item.Pattern)
		if err != nil && strings.TrimSpace(item.Pattern) != "" {
			problems = append(problems, label+": pattern does not compile under RE2: "+err.Error())
		} else if err == nil && compiled.MatchString("") {
			problems = append(problems, label+": pattern matches the empty string")
		}
		set.Rules = append(set.Rules, Rule{
			ID: item.ID, Class: class, Pattern: item.Pattern,
			Rationale: item.Rationale, Example: item.Example, Clean: item.Clean,
			re: compiled,
		})
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New("slop rules: " + strings.Join(problems, "; "))
	}
	return set, nil
}

// Rule returns the rule with the given id.
func (s *RuleSet) Rule(id string) (Rule, bool) {
	for _, rule := range s.Rules {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// Matches reports whether the rule's pattern matches text as written. It is
// the primitive the test suite uses to check each rule's example and clean
// near-miss; scanning goes through Scan, which also masks quoted spans.
func (r Rule) Matches(text string) bool {
	return r.re != nil && r.re.MatchString(text)
}
