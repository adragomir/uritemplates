// Copyright 2013 Joshua Tacoma. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package uritemplates is a level 4 implementation of RFC 6570 (URI
// Template).
//
// To use uritemplates, parse a template string and expand it with a value
// map:
//
//	template, _ := uritemplates.Parse("https://api.github.com/repos{/user,repo}")
//	values := make(map[string]interface{})
//	values["user"] = "jtacoma"
//	values["repo"] = "uritemplates"
//	expanded, _ := template.ExpandString(values)
//	fmt.Printf(expanded)
//
package uritemplates

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"net/url"
)

var (
	ALPHA = "a-zA_Z"
	DIGIT = "0-9"
	GEN_DELIMS = ":/?#[\\]@"
	SUB_DELIMS = "!$&'()*+,;="
	UNRESERVED = ALPHA + DIGIT + "\\-._~"
	RESERVED = GEN_DELIMS + SUB_DELIMS
	UNRESERVED_RE = "(?:[" + UNRESERVED + "]|%[0-9A-Fa-f][0-9A-Fa-f])"
	RESERVED_RE = "(?:[" + UNRESERVED + RESERVED + "]|%[0-9A-Fa-f][0-9A-Fa-f])"

	nonUnreserved = regexp.MustCompile("[^A-Za-z0-9\\-._~]")
	nonReserved   = regexp.MustCompile("[^A-Za-z0-9\\-._~:/?#[\\]@!$&'()*+,;=]")
	validname  = regexp.MustCompile("^([A-Za-z0-9_\\.]|%[0-9A-Fa-f][0-9A-Fa-f])+$")
	hex        = []byte("0123456789ABCDEF")
)

func pctEncode(src []byte) []byte {
	dst := make([]byte, len(src)*3)
	for i, b := range src {
		buf := dst[i*3 : i*3+3]
		buf[0] = 0x25
		buf[1] = hex[b/16]
		buf[2] = hex[b%16]
	}
	return dst
}

func escape(s string, allowReserved bool) (escaped string) {
	if allowReserved {
		escaped = string(nonReserved.ReplaceAllFunc([]byte(s), pctEncode))
	} else {
		escaped = string(nonUnreserved.ReplaceAllFunc([]byte(s), pctEncode))
	}
	return escaped
}

// A UriTemplate is a parsed representation of a URI template.
type UriTemplate struct {
	raw   string
	parts []templatePart
}

// Parse parses a URI template string into a UriTemplate object.
func Parse(rawtemplate string) (template *UriTemplate, err error) {
	template = new(UriTemplate)
	template.raw = rawtemplate
	split := strings.Split(rawtemplate, "{")
	template.parts = make([]templatePart, len(split)*2-1)
	for i, s := range split {
		if i == 0 {
			if strings.Contains(s, "}") {
				err = errors.New("unexpected }")
				break
			} else {
				subsplit := strings.Split(s, ":")
				if len(subsplit) > 1 && strings.Contains(subsplit[0], "/") {
					err = errors.New("unexpected :")
					break
				}
			}
			template.parts[i].raw = s
		} else {
			subsplit := strings.Split(s, "}")
			if len(subsplit) != 2 {
				err = errors.New("malformed template")
				break
			}
			expression := subsplit[0]
			template.parts[i*2-1], err = parseExpression(expression)
			if err != nil {
				break
			}
			if strings.Contains(subsplit[1], "}") {
				err = errors.New("unexpected }")
				break
			}
			template.parts[i*2].raw = subsplit[1]
		}
	}
	if err != nil {
		template = nil
	}
	return template, err
}

type templatePart struct {
	raw           string
	terms         []templateTerm
	first         string
	sep           string
	named         bool
	ifemp         string
	allowReserved bool
}

type templateTerm struct {
	name     string
	explode  bool
	truncate int
}

func parseExpression(expression string) (result templatePart, err error) {
	switch expression[0] {
	case '+':
		result.sep = ","
		result.allowReserved = true
		expression = expression[1:]
	case '.':
		result.first = "."
		result.sep = "."
		expression = expression[1:]
	case '/':
		result.first = "/"
		result.sep = "/"
		expression = expression[1:]
	case ';':
		result.first = ";"
		result.sep = ";"
		result.named = true
		expression = expression[1:]
	case '?':
		result.first = "?"
		result.sep = "&"
		result.named = true
		result.ifemp = "="
		expression = expression[1:]
	case '&':
		result.first = "&"
		result.sep = "&"
		result.named = true
		result.ifemp = "="
		expression = expression[1:]
	case '#':
		result.first = "#"
		result.sep = ","
		result.allowReserved = true
		expression = expression[1:]
	default:
		result.sep = ","
	}
	rawterms := strings.Split(expression, ",")
	result.terms = make([]templateTerm, len(rawterms))
	for i, raw := range rawterms {
		result.terms[i], err = parseTerm(raw)
		if err != nil {
			break
		}
	}
	return result, err
}

func parseTerm(term string) (result templateTerm, err error) {
	if strings.HasSuffix(term, "*") {
		result.explode = true
		term = term[:len(term)-1]
	}
	split := strings.Split(term, ":")
	if len(split) == 1 {
		result.name = term
	} else if len(split) == 2 {
		result.name = split[0]
		var parsed int64
		parsed, err = strconv.ParseInt(split[1], 10, 0)
		result.truncate = int(parsed)
	} else {
		err = errors.New("multiple colons in same term")
	}
	if !validname.MatchString(result.name) {
		err = errors.New("not a valid name: " + result.name)
	}
	if result.explode && result.truncate > 0 {
		err = errors.New("both explode and prefix modifers on same term")
	}
	return result, err
}

// Expand expands a URI template with a set of values to produce a string.
func (self *UriTemplate) Expand(values map[string]interface{}) (result string, err error) {
	var next string
	for _, p := range self.parts {
		next, err = p.expand(values)
		if err != nil {
			break
		}
		result += next
	}
	return result, err
}

func unescapeArr(escaped []string) (unescaped []string) {
	unescaped = make([]string, len(escaped))
	for ii, tmp := range escaped {
		unescaped[ii], _ = url.QueryUnescape(tmp)
	}
	return unescaped
}

func (self *UriTemplate) Unexpand(uri string) (result map[string]interface{}, err error) {
	restr := "^"
	fmt.Printf("Unexpanding template: %+v", self.parts)
	for _, p := range self.parts {
		restr += p.buildRegexp()
	}
	restr += "$"
	fmt.Printf("regexp: %s\n", restr)
	matches, err := regexp.MatchString(restr, uri)
	if (!matches) {
		return nil, errors.New("No match")
	}
	pieces := regexp.MustCompile(restr).FindStringSubmatch(uri)[1:]
	index := 0
	fmt.Printf("%d, %+v\n", len(pieces), pieces)
	out := make(map[string]interface{})
	for _, p := range self.parts {
		if p.raw != "" || (p.raw == "" && len(p.terms) == 0) {
			continue
		}
		for _, t := range p.terms {
			// +, #, /, .
			if p.allowReserved || (p.sep == "/" || p.sep == "." || p.sep == ",") {
				value := pieces[index]
				if value != "" && t.explode {
					out[t.name] = unescapeArr(strings.Split(value, p.sep))
				} else {
					out[t.name] = value
				}
			} else if p.sep == ";" || p.sep == "&" {
				if t.explode {
					hash := make(map[string]string)
					for _, v := range strings.Split(pieces[index], p.sep) {
						kv := strings.Split(v, "=")
						if len(kv) == 2 {
							hash[kv[0]], _ = url.QueryUnescape(kv[1])
						} else {
							hash[kv[0]] = ""
						}
					}
					out[t.name] = hash
				} else {
					nv := strings.Split(pieces[index], "=")
					if len(nv) == 2 {
						out[nv[0]] = nv[1]
					} else {
						out[nv[0]] = ""
					}
				}
			}
			index ++
		}
	}
	return out, nil
}

func (self *templatePart) expand(values map[string]interface{}) (result string, err error) {
	if len(self.raw) > 0 {
		return self.raw, err
	}
	result = self.first
	for _, term := range self.terms {
		value, exists := values[term.name]
		if !exists {
			continue
		}
		var next string
		switch value.(type) {
		case string:
			v := value.(string)
			next = self.expandString(term, v)
		case []interface{}:
			v := value.([]interface{})
			next = self.expandArray(term, v)
		case map[string]interface{}:
			if term.truncate > 0 {
				err = errors.New("cannot truncate a map expansion")
				break
			}
			v := value.(map[string]interface{})
			next = self.expandMap(term, v)
		default:
			v := fmt.Sprintf("%v", value)
			next = self.expandString(term, v)
		}
		if result != self.first {
			result += self.sep
		}
		result += next
	}
	if result == self.first {
		result = ""
	}
	return result, err
}

func(self *templatePart) buildRegexp() string {
	if self.raw != "" {
		return regexp.QuoteMeta(self.raw)
	}
	restr := regexp.QuoteMeta(self.first)
	varspecs := make([]string, len(self.terms), len(self.terms))
	for idx, t := range self.terms {
		group := ""
		if self.allowReserved {
			group = RESERVED_RE + "*?"
		} else {
			switch self.sep {
			case "/": group = UNRESERVED_RE + "*?"
			case ".": group = strings.Replace(UNRESERVED_RE, "\\.", "", -1) + "*?"
			case ";": group = UNRESERVED_RE + "*=?" + UNRESERVED_RE + "*?"
			case "?", "&": group = UNRESERVED_RE + "*=" + UNRESERVED_RE + "*?"
			default: group = UNRESERVED_RE + "*?"
			}
		}
		if t.explode {
			group = "(" + group + "(?:" + regexp.QuoteMeta(self.sep) + "?" + group + ")*)?"
		} else {
			group = "(" + group + ")?"
		}
		varspecs[idx] = group
	}
	return restr + strings.Join(varspecs, regexp.QuoteMeta(self.sep))
}

func (self *templatePart) expandName(name string, empty bool) (result string) {
	if self.named {
		result = name
		if empty {
			result += self.ifemp
		} else {
			result += "="
		}
	}
	return result
}

func (self *templatePart) expandString(t templateTerm, s string) (result string) {
	if len(s) > t.truncate && t.truncate > 0 {
		s = s[:t.truncate]
	}
	return self.expandName(t.name, len(s) == 0) +
		escape(s, self.allowReserved)
}

func (self *templatePart) expandArray(t templateTerm, a []interface{}) (result string) {
	if !t.explode {
		result = self.expandName(t.name, len(a) == 0)
	}
	for i, v := range a {
		if t.explode && i > 0 {
			result += self.sep
		} else if i > 0 {
			result += ","
		}
		var s string
		switch v.(type) {
		case string:
			s = v.(string)
		default:
			s = fmt.Sprintf("%v", v)
		}
		if len(s) > t.truncate && t.truncate > 0 {
			s = s[:t.truncate]
		}
		if self.named && t.explode {
			result += self.expandName(t.name, len(s) == 0)
		}
		result += escape(s, self.allowReserved)
	}
	return result
}

func (self *templatePart) expandMap(t templateTerm, m map[string]interface{}) (result string) {
	for k, v := range m {
		if t.explode && len(result) > 0 {
			result += self.sep
		} else if len(result) > 0 {
			result += ","
		}
		var s string
		switch v.(type) {
		case string:
			s = v.(string)
		default:
			s = fmt.Sprintf("%v", v)
		}
		if len(s) > t.truncate && t.truncate > 0 {
			s = s[:t.truncate]
		}
		if t.explode {
			result += escape(k, self.allowReserved) +
				"=" + escape(s, self.allowReserved)
		} else {
			result += escape(k, self.allowReserved) +
				"," + escape(s, self.allowReserved)
		}
	}
	if !t.explode {
		result = self.expandName(t.name, len(m) == 0) + result
	}
	return result
}
