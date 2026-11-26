package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// evalExpr evaluates arithmetic with + - * / % ^, parentheses, constants and
// a few functions. It is a small recursive-descent parser: no eval, no code.
func evalExpr(s string) (float64, error) {
	p := &calcParser{s: strings.ReplaceAll(s, "**", "^")}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	p.skip()
	if p.i < len(p.s) {
		return 0, fmt.Errorf("unexpected %q at position %d", p.s[p.i], p.i)
	}
	return v, nil
}

type calcParser struct {
	s string
	i int
}

func (p *calcParser) skip() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *calcParser) peek() byte {
	p.skip()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *calcParser) expr() (float64, error) {
	v, err := p.term()
	for err == nil {
		switch p.peek() {
		case '+', '-':
			op := p.s[p.i]
			p.i++
			r, e := p.term()
			if e != nil {
				return 0, e
			}
			if op == '+' {
				v += r
			} else {
				v -= r
			}
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *calcParser) term() (float64, error) {
	v, err := p.power()
	for err == nil {
		switch p.peek() {
		case '*', '/', '%':
			op := p.s[p.i]
			p.i++
			r, e := p.power()
			if e != nil {
				return 0, e
			}
			switch op {
			case '*':
				v *= r
			case '/':
				if r == 0 {
					return 0, fmt.Errorf("division by zero")
				}
				v /= r
			default:
				v = math.Mod(v, r)
			}
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *calcParser) power() (float64, error) {
	base, err := p.unary()
	if err != nil {
		return 0, err
	}
	if p.peek() == '^' {
		p.i++
		exp, err := p.power() // right associative
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *calcParser) unary() (float64, error) {
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		return -v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.primary()
}

var calcFuncs = map[string]func(float64) float64{
	"sqrt": math.Sqrt, "abs": math.Abs, "round": math.Round, "floor": math.Floor, "ceil": math.Ceil,
	"ln": math.Log, "log": math.Log10, "log2": math.Log2, "exp": math.Exp,
	"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
}

func (p *calcParser) primary() (float64, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.i++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("missing )")
		}
		p.i++
		return v, nil
	case c >= '0' && c <= '9' || c == '.':
		start := p.i
		for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.' || p.s[p.i] == '_') {
			p.i++
		}
		if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
			p.i++
			if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
				p.i++
			}
			for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
				p.i++
			}
		}
		return strconv.ParseFloat(strings.ReplaceAll(p.s[start:p.i], "_", ""), 64)
	case unicode.IsLetter(rune(c)):
		start := p.i
		for p.i < len(p.s) && (unicode.IsLetter(rune(p.s[p.i])) || unicode.IsDigit(rune(p.s[p.i]))) {
			p.i++
		}
		name := strings.ToLower(p.s[start:p.i])
		switch name {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		}
		fn, ok := calcFuncs[name]
		if !ok {
			return 0, fmt.Errorf("unknown function %q", name)
		}
		if p.peek() != '(' {
			return 0, fmt.Errorf("%s needs parentheses", name)
		}
		v, err := p.primary()
		if err != nil {
			return 0, err
		}
		return fn(v), nil
	}
	if c == 0 {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	return 0, fmt.Errorf("unexpected %q", c)
}
