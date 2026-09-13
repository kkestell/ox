// Package shellrules matches shell commands against activation-scoped rules.
// A rule is a whitespace-split word sequence, and every command invocation in
// the POSIX parse tree must match at least one granted rule.
package shellrules

import (
	"path"
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

// indirectPrograms run a program chosen by their arguments. A rule naming one
// of them authorizes whatever the next command asks it to run, so no reusable
// rule can stand in for the command the user approved.
var indirectPrograms = map[string]bool{
	"ash": true, "bash": true, "csh": true, "dash": true, "fish": true,
	"ksh": true, "sh": true, "tcsh": true, "zsh": true,

	"bun": true, "deno": true, "lua": true, "node": true, "nodejs": true,
	"osascript": true, "perl": true, "php": true, "python": true,
	"python2": true, "python3": true, "ruby": true, "tclsh": true,

	"chroot": true, "command": true, "doas": true, "env": true, "eval": true,
	"exec": true, "ionice": true, "nice": true, "nohup": true, "rsh": true,
	"script": true, "setsid": true, "ssh": true, "stdbuf": true, "su": true,
	"sudo": true, "time": true, "timeout": true, "watch": true, "xargs": true,
}

// Suggest derives a reusable rule from the command's first invocation, or ""
// when no rule can stand in for the command. Because Allowed matches a rule as
// a word prefix, a rule is only safe when every word of the invocation is a
// literal the user could read and the program is not one that runs another
// program of its arguments' choosing.
func Suggest(command string) string {
	call := firstCall(command)
	if call == nil || len(call.Args) == 0 {
		return ""
	}
	words := make([]string, 0, len(call.Args))
	for _, argument := range call.Args {
		value, ok := literalValue(argument)
		if !ok || value == "" {
			return ""
		}
		words = append(words, value)
	}
	if indirectPrograms[path.Base(words[0])] {
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
