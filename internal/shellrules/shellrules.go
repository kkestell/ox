// Package shellrules matches shell commands against activation-scoped rules.
// A rule is a whitespace-split word sequence, and every command invocation in
// the POSIX parse tree must match at least one granted rule.
package shellrules

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Allowed reports whether every invocation in command is covered by rules.
// Anything the POSIX parser or literal matcher cannot settle fails closed.
func Allowed(rules []string, command string) bool {
	file, ok := parse(command)
	if !ok {
		return false
	}
	allowed := true
	sawCall := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if !allowed {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		sawCall = true
		if !callMatches(rules, call) {
			allowed = false
			return false
		}
		return true
	})
	return allowed && sawCall
}

func callMatches(rules []string, call *syntax.CallExpr) bool {
	if len(call.Assigns) > 0 || len(call.Args) == 0 {
		return false
	}
	for _, rule := range rules {
		if ruleMatchesCall(rule, call) {
			return true
		}
	}
	return false
}

func ruleMatchesCall(rule string, call *syntax.CallExpr) bool {
	words := strings.Fields(rule)
	if len(words) == 0 || len(call.Args) < len(words) {
		return false
	}
	for index, word := range words {
		if word == "*" {
			continue
		}
		value, ok := literalValue(call.Args[index])
		if !ok || value != word {
			return false
		}
	}
	return true
}

// Suggest derives a conservative rule from the command's first invocation.
func Suggest(command string) string {
	var words []string
	if call := firstCall(command); call != nil {
		for _, argument := range call.Args {
			value, ok := literalValue(argument)
			if !ok {
				break
			}
			words = append(words, value)
		}
	}
	if len(words) == 0 {
		words = strings.Fields(command)
	}
	if len(words) == 0 {
		return ""
	}
	if len(words) >= 2 && !strings.HasPrefix(words[1], "-") {
		return words[0] + " " + words[1]
	}
	return words[0]
}

func firstCall(command string) *syntax.CallExpr {
	file, ok := parse(command)
	if !ok {
		return nil
	}
	var found *syntax.CallExpr
	syntax.Walk(file, func(node syntax.Node) bool {
		if found != nil {
			return false
		}
		if call, ok := node.(*syntax.CallExpr); ok {
			found = call
			return false
		}
		return true
	})
	return found
}

func parse(command string) (*syntax.File, bool) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) == 0 {
		return nil, false
	}
	return file, true
}

func literalValue(word *syntax.Word) (string, bool) {
	var value strings.Builder
	for _, raw := range word.Parts {
		switch part := raw.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(part.Value, "*?[") {
				return "", false
			}
			value.WriteString(part.Value)
		case *syntax.SglQuoted:
			value.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, quoted := range part.Parts {
				literal, ok := quoted.(*syntax.Lit)
				if !ok {
					return "", false
				}
				value.WriteString(literal.Value)
			}
		default:
			return "", false
		}
	}
	return value.String(), true
}
