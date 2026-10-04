package tools

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

// globMatcher matches workspace-relative file paths the way ripgrep's `--glob`
// does. Given to ripgrep, a glob would override its ignore filtering, so
// `**/*` would list `.git` and every ignored file.
type globMatcher struct {
	re *regexp.Regexp
	// basename is set for a glob without a slash, which matches a file name
	// at any depth.
	basename bool
	// negated is set for a `!` glob, which matches the files it does not.
	negated bool
}

func compileGlob(glob string) (*globMatcher, error) {
	matcher := &globMatcher{}
	glob, matcher.negated = strings.CutPrefix(glob, "!")
	matcher.basename = !strings.Contains(strings.TrimSuffix(glob, "/"), "/")
	glob = strings.TrimPrefix(glob, "/")
	expression, _, err := translate(glob, false)
	if err != nil {
		return nil, err
	}
	if matcher.re, err = regexp.Compile("^" + expression + "$"); err != nil {
		return nil, err
	}
	return matcher, nil
}

// match reports whether the glob selects a path such as `./src/main.rs`.
func (g *globMatcher) match(name string) bool {
	name = strings.TrimPrefix(name, "./")
	if g.basename {
		name = path.Base(name)
	}
	return g.re.MatchString(name) != g.negated
}

// translate converts glob syntax to a regular expression. Inside an alternate
// group it stops at the group's `,` or `}` and returns the rest.
func translate(glob string, inGroup bool) (string, string, error) {
	var out strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if strings.HasPrefix(glob[i:], "**") {
				start := i == 0 || glob[i-1] == '/'
				end := i+2 == len(glob) || glob[i+2] == '/'
				if start && end {
					if i+2 == len(glob) {
						out.WriteString(".*")
					} else {
						out.WriteString("(?:.*/)?")
						i++
					}
					i++
					continue
				}
			}
			out.WriteString("[^/]*")
		case '?':
			out.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end == 0 {
				// A leading `]` is a literal member of the class.
				end = strings.IndexByte(glob[i+2:], ']') + 1
			}
			if end <= 0 {
				return "", "", errors.New("unclosed character class; missing ']'")
			}
			class := glob[i+1 : i+1+end]
			out.WriteString("[")
			if class[0] == '!' || class[0] == '^' {
				out.WriteString("^")
				class = class[1:]
			}
			out.WriteString(strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `^`, `\^`).Replace(class))
			out.WriteString("]")
			i += end + 1
		case '{':
			if inGroup {
				return "", "", errors.New("nested alternate groups are not allowed")
			}
			var alternatives []string
			rest := glob[i+1:]
			for {
				alternative, remaining, err := translate(rest, true)
				if err != nil {
					return "", "", err
				}
				alternatives = append(alternatives, alternative)
				if remaining == "" {
					return "", "", errors.New("unclosed alternate group; missing '}'")
				}
				rest = remaining[1:]
				if remaining[0] == '}' {
					break
				}
			}
			out.WriteString("(?:" + strings.Join(alternatives, "|") + ")")
			i = len(glob) - len(rest) - 1
		case ',', '}':
			if inGroup {
				return out.String(), glob[i:], nil
			}
			out.WriteString(regexp.QuoteMeta(string(c)))
		case '\\':
			if i+1 < len(glob) {
				i++
			}
			out.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		default:
			out.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return out.String(), "", nil
}
