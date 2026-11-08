package gmail

import (
	"strconv"
	"strings"
	"time"
)

// Gmail search syntax: adjacent terms AND together, OR (or a {braced} group)
// alternates, parentheses group, a leading '-' negates, and "quotes" keep a
// phrase whole. Operators the sandbox does not model match every message, as
// before, so a scenario never fails on an unfamiliar operator.
//
// Calendar dates in after:/before: are read as UTC midnight; Gmail uses the
// account's time zone. Epoch seconds are exact.

type searchQuery struct{ root queryNode }

type queryNode interface {
	matches(mailMessage, time.Time) bool
	labels() []string
}

type andNode []queryNode
type orNode []queryNode
type notNode struct{ node queryNode }
type termNode struct{ operator, value string }

func parseQuery(raw string) searchQuery {
	p := queryParser{tokens: lexQuery(raw)}
	return searchQuery{root: p.sequence("")}
}

func (q searchQuery) matches(message mailMessage, now time.Time) bool {
	return q.root.matches(message, now)
}

// wantsTrash reports whether the query names trash or spam itself, which
// Gmail treats as including those messages without includeSpamTrash.
func (q searchQuery) wantsTrash() bool {
	for _, label := range q.root.labels() {
		switch strings.ToLower(label) {
		case "trash", "spam", "anywhere":
			return true
		}
	}
	return false
}

func (n andNode) matches(message mailMessage, now time.Time) bool {
	for _, child := range n {
		if !child.matches(message, now) {
			return false
		}
	}
	return true
}

func (n orNode) matches(message mailMessage, now time.Time) bool {
	for _, child := range n {
		if child.matches(message, now) {
			return true
		}
	}
	return len(n) == 0
}

func (n notNode) matches(message mailMessage, now time.Time) bool {
	return !n.node.matches(message, now)
}

func (n andNode) labels() []string { return childLabels(n) }
func (n orNode) labels() []string  { return childLabels(n) }
func (n notNode) labels() []string { return nil }
func (n termNode) labels() []string {
	if n.operator == "in" || n.operator == "label" {
		return []string{n.value}
	}
	return nil
}

func childLabels(nodes []queryNode) []string {
	var out []string
	for _, node := range nodes {
		out = append(out, node.labels()...)
	}
	return out
}

func (n termNode) matches(message mailMessage, now time.Time) bool {
	value := strings.ToLower(n.value)
	switch n.operator {
	case "":
		return strings.Contains(strings.ToLower(message.Subject+" "+message.Body), value)
	case "is":
		switch value {
		case "unread":
			return message.hasLabel("UNREAD")
		case "read":
			return !message.hasLabel("UNREAD")
		case "starred":
			return message.hasLabel("STARRED")
		case "important":
			return message.hasLabel("IMPORTANT")
		}
	case "in", "label":
		if value == "anywhere" {
			return true
		}
		return hasLabelFold(message, n.value)
	case "from":
		return strings.Contains(strings.ToLower(message.From), value)
	case "to":
		return strings.Contains(strings.ToLower(message.To), value)
	case "subject":
		return strings.Contains(strings.ToLower(message.Subject), value)
	case "after":
		if at, ok := queryTime(value); ok {
			return message.InternalDate >= at.UnixMilli()
		}
	case "before":
		if at, ok := queryTime(value); ok {
			return message.InternalDate < at.UnixMilli()
		}
	case "newer_than":
		if age, ok := queryAge(value, now); ok {
			return message.InternalDate >= age.UnixMilli()
		}
	case "older_than":
		if age, ok := queryAge(value, now); ok {
			return message.InternalDate < age.UnixMilli()
		}
	}
	return true
}

// queryTime reads epoch seconds or a YYYY/MM/DD (or YYYY-MM-DD) date.
func queryTime(value string) (time.Time, bool) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(seconds, 0), true
	}
	for _, layout := range []string{"2006/1/2", "2006-1-2"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// queryAge reads newer_than/older_than values such as 2d, 3m and 1y.
func queryAge(value string, now time.Time) (time.Time, bool) {
	if len(value) < 2 {
		return time.Time{}, false
	}
	count, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || count < 0 {
		return time.Time{}, false
	}
	switch value[len(value)-1] {
	case 'd':
		return now.AddDate(0, 0, -count), true
	case 'm':
		return now.AddDate(0, -count, 0), true
	case 'y':
		return now.AddDate(-count, 0, 0), true
	}
	return time.Time{}, false
}

type queryToken struct {
	text   string
	symbol bool // one of ( ) { }
	phrase bool // the word opened with a quote, so it is a phrase, never an operator
}

func lexQuery(raw string) []queryToken {
	var tokens []queryToken
	runes := []rune(raw)
	for i := 0; i < len(runes); {
		switch r := runes[i]; {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '(' || r == ')' || r == '{' || r == '}':
			tokens = append(tokens, queryToken{text: string(r), symbol: true})
			i++
		default:
			var word strings.Builder
			phrase := runes[i] == '"'
			for i < len(runes) {
				r := runes[i]
				if r == '"' {
					i++
					for i < len(runes) && runes[i] != '"' {
						word.WriteRune(runes[i])
						i++
					}
					i++ // closing quote, if present
					continue
				}
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ')' || r == '{' || r == '}' {
					break
				}
				word.WriteRune(r)
				i++
			}
			if word.Len() > 0 {
				tokens = append(tokens, queryToken{text: word.String(), phrase: phrase})
			}
		}
	}
	return tokens
}

type queryParser struct {
	tokens []queryToken
	pos    int
}

func (p *queryParser) peek() (queryToken, bool) {
	if p.pos >= len(p.tokens) {
		return queryToken{}, false
	}
	return p.tokens[p.pos], true
}

// sequence parses terms up to the closing symbol (or the end). Inside braces
// the terms alternate; elsewhere they AND, with OR splitting alternatives.
func (p *queryParser) sequence(closing string) queryNode {
	var alternatives orNode
	var current andNode
	flush := func() {
		if len(current) > 0 {
			alternatives = append(alternatives, current)
			current = nil
		}
	}
	for {
		token, ok := p.peek()
		if !ok {
			break
		}
		if token.symbol && (token.text == ")" || token.text == "}") {
			p.pos++
			if token.text == closing {
				break
			}
			continue // an unmatched closer is ignored, as Gmail does
		}
		if !token.symbol && !token.phrase && (token.text == "OR" || token.text == "|") {
			p.pos++
			flush()
			continue
		}
		if !token.symbol && !token.phrase && token.text == "AND" {
			p.pos++
			continue
		}
		node := p.unary()
		if node == nil {
			continue
		}
		if closing == "}" {
			alternatives = append(alternatives, node)
		} else {
			current = append(current, node)
		}
	}
	flush()
	switch len(alternatives) {
	case 0:
		return andNode{}
	case 1:
		return alternatives[0]
	}
	return alternatives
}

func (p *queryParser) unary() queryNode {
	token := p.tokens[p.pos]
	p.pos++
	if token.symbol {
		if token.text == "(" {
			return p.sequence(")")
		}
		return p.sequence("}")
	}
	if !token.phrase && strings.HasPrefix(token.text, "-") {
		rest := strings.TrimPrefix(token.text, "-")
		if rest == "" {
			if next, ok := p.peek(); ok && next.symbol && (next.text == "(" || next.text == "{") {
				return notNode{node: p.unary()}
			}
			return nil
		}
		return notNode{node: queryTerm(queryToken{text: rest})}
	}
	return queryTerm(token)
}

func queryTerm(token queryToken) queryNode {
	if token.phrase {
		return termNode{value: token.text}
	}
	if operator, value, ok := strings.Cut(token.text, ":"); ok && operator != "" {
		operator = strings.ToLower(operator)
		switch operator {
		case "is", "in", "label", "from", "to", "subject", "after", "before", "newer_than", "older_than":
			return termNode{operator: operator, value: value}
		}
		return andNode{} // an operator the sandbox does not model
	}
	return termNode{value: token.text}
}
