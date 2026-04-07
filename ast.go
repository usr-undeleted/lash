package main

import "strings"

type Node interface {
	nodeType() string
}

type Program struct {
	Commands []Node
}

func (p *Program) nodeType() string { return "Program" }

type AndOr struct {
	Left  Node
	Op    string
	Right Node
}

func (a *AndOr) nodeType() string { return "AndOr" }

type Pipeline struct {
	Commands []Node
	Negated  bool
}

func (p *Pipeline) nodeType() string { return "Pipeline" }

type Command struct {
	Assignments  []Assignment
	Args         []string
	Redirections []Redir
	Background   bool
}

func (c *Command) nodeType() string { return "Command" }

type IfStmt struct {
	Condition Node
	Body      Node
	Elifs     []ElifBranch
	Else      Node
}

func (i *IfStmt) nodeType() string { return "IfStmt" }

type ElifBranch struct {
	Condition Node
	Body      Node
}

type WhileStmt struct {
	Condition Node
	Body      Node
	Until     bool
}

func (w *WhileStmt) nodeType() string { return "WhileStmt" }

type ForStmt struct {
	Var   string
	Words []string
	Body  Node
}

func (f *ForStmt) nodeType() string { return "ForStmt" }

type CStyleForStmt struct {
	Init string
	Cond string
	Step string
	Body Node
}

func (c *CStyleForStmt) nodeType() string { return "CStyleForStmt" }

type SelectStmt struct {
	Var   string
	Words []string
	Body  Node
}

func (s *SelectStmt) nodeType() string { return "SelectStmt" }

type BreakStmt struct{}

func (b *BreakStmt) nodeType() string { return "BreakStmt" }

type ContinueStmt struct{}

func (c *ContinueStmt) nodeType() string { return "ContinueStmt" }

type FuncDef struct {
	Name string
	Body Node
}

func (f *FuncDef) nodeType() string { return "FuncDef" }

type CaseStmt struct {
	Word     string
	Branches []CaseBranch
}

func (c *CaseStmt) nodeType() string { return "CaseStmt" }

type CaseBranch struct {
	Patterns []string
	Body     Node
}

type Subshell struct {
	Body Node
}

func (s *Subshell) nodeType() string { return "Subshell" }

type Group struct {
	Body Node
}

func (g *Group) nodeType() string { return "Group" }

type Assignment struct {
	Name  string
	Value string
}

type Redir struct {
	Op     string
	Target string
}

type parser struct {
	tokens []string
	pos    int
}

func (p *parser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *parser) peekAt(offset int) string {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return ""
	}
	return p.tokens[idx]
}

func (p *parser) advance() string {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) match(token string) bool {
	if p.peek() == token {
		p.advance()
		return true
	}
	return false
}

var allKeywords = []string{
	"if", "then", "elif", "else", "fi",
	"while", "until", "for", "in", "do", "done",
	"case", "esac", "select", "break", "continue",
	"function",
}

func isKeyword(t string) bool {
	for _, kw := range allKeywords {
		if kw == t {
			return true
		}
	}
	return false
}

func (p *parser) isKeyword(t string) bool {
	return isKeyword(t)
}

func (p *parser) isOperator(t string) bool {
	switch t {
	case ";", "&&", "||", "|", "&", ">>", ">", "<", "<<", "<<-", "<<<":
		return true
	}
	return false
}

func Parse(input string) *Program {
	tokens := tokenize(input)
	p := &parser{tokens: tokens}
	return p.parseProgram()
}

func (p *parser) parseProgram() *Program {
	prog := &Program{}
	for p.pos < len(p.tokens) {
		if p.match(";") {
			continue
		}
		node := p.parseAndOr()
		if node != nil {
			prog.Commands = append(prog.Commands, node)
		} else {
			p.advance()
		}
	}
	return prog
}

func (p *parser) parseAndOr() Node {
	left := p.parsePipeline()
	if left == nil {
		return nil
	}
	for p.peek() == "&&" || p.peek() == "||" {
		op := p.advance()
		right := p.parsePipeline()
		if right == nil {
			break
		}
		left = &AndOr{Left: left, Op: op, Right: right}
	}
	return left
}

func (p *parser) parsePipeline() Node {
	var negated bool
	if p.peek() == "!" && !p.isKeyword(p.peekAt(1)) && !p.isOperator(p.peekAt(1)) {
		p.advance()
		negated = true
	}

	cmd := p.parseCommand()
	if cmd == nil {
		return nil
	}

	cmds := []Node{cmd}
	for p.peek() == "|" {
		p.advance()
		next := p.parseCommand()
		if next == nil {
			break
		}
		cmds = append(cmds, next)
	}

	if len(cmds) == 1 && !negated {
		return cmds[0]
	}
	return &Pipeline{Commands: cmds, Negated: negated}
}

func (p *parser) parseCommand() Node {
	if p.peek() == "if" {
		return p.parseIf()
	}
	if p.peek() == "while" || p.peek() == "until" {
		return p.parseWhile()
	}
	if p.peek() == "for" {
		return p.parseFor()
	}
	if p.peek() == "select" {
		return p.parseSelect()
	}
	if p.peek() == "case" {
		return p.parseCase()
	}
	if p.peek() == "break" {
		p.advance()
		return &BreakStmt{}
	}
	if p.peek() == "continue" {
		p.advance()
		return &ContinueStmt{}
	}
	if p.peek() == "function" {
		return p.parseFuncDef()
	}
	if p.isFuncDefPattern() {
		return p.parseFuncDef()
	}
	if p.peek() == "(" {
		return p.parseSubshell()
	}
	if p.peek() == "{" {
		return p.parseGroup()
	}

	return p.parseSimpleCommand()
}

func (p *parser) parseSimpleCommand() Node {
	cmd := &Command{}

	for p.pos < len(p.tokens) {
		t := p.peek()
		if t == ";" || t == ";;" || t == "&&" || t == "||" || t == "|" || t == ")" || t == "}" || t == "(" {
			break
		}
		if len(cmd.Args) == 0 && p.isCompoundEnd(t) {
			break
		}

		if t == "&" {
			if p.peekAt(1) == "&" {
				break
			}
			p.advance()
			cmd.Background = true
			break
		}

		if t == ">" || t == ">>" || t == "<" || t == "<<" || t == "<<-" || t == "<<<" {
			op := p.advance()
			target := p.advance()
			if target == "" || target == ";" || target == "&&" || target == "||" || target == "|" || target == ")" || target == "}" {
				p.pos--
				cmd.Args = append(cmd.Args, op)
				continue
			}
			cmd.Redirections = append(cmd.Redirections, Redir{Op: op, Target: target})
			continue
		}

		if eqIdx := strings.Index(t, "="); eqIdx > 0 && len(cmd.Args) == 0 {
			name := t[:eqIdx]
			if isValidVarName(name) {
				p.advance()
				cmd.Assignments = append(cmd.Assignments, Assignment{
					Name:  name,
					Value: t[eqIdx+1:],
				})
				continue
			}
		}

		p.advance()
		cmd.Args = append(cmd.Args, t)
	}

	if len(cmd.Args) == 0 && len(cmd.Assignments) == 0 {
		return nil
	}

	return cmd
}

func (p *parser) isCompoundEnd(t string) bool {
	switch t {
	case "then", "elif", "else", "fi", "do", "done", "esac":
		return true
	}
	return false
}

func (p *parser) skipSemicolons() {
	for p.peek() == ";" {
		p.advance()
	}
}

func (p *parser) parseIf() Node {
	p.advance()

	condition := p.parseAndOr()
	p.skipSemicolons()
	if !p.match("then") {
		return &IfStmt{Condition: condition, Body: nil}
	}

	body := p.parseCompoundBody("elif", "else", "fi")

	var elifs []ElifBranch
	for p.peek() == "elif" {
		p.advance()
		elifCond := p.parseAndOr()
		p.skipSemicolons()
		p.match("then")
		elifBody := p.parseCompoundBody("elif", "else", "fi")
		elifs = append(elifs, ElifBranch{Condition: elifCond, Body: elifBody})
	}

	var elseBody Node
	p.skipSemicolons()
	if p.match("else") {
		elseBody = p.parseCompoundBody("fi")
	}

	p.skipSemicolons()
	p.match("fi")

	return &IfStmt{
		Condition: condition,
		Body:      body,
		Elifs:     elifs,
		Else:      elseBody,
	}
}

func (p *parser) parseWhile() Node {
	until := false
	if p.peek() == "until" {
		until = true
	}
	p.advance()

	condition := p.parseAndOr()
	p.skipSemicolons()
	if !p.match("do") {
		return &WhileStmt{Condition: condition, Until: until}
	}

	body := p.parseCompoundBody("done")
	p.skipSemicolons()
	p.match("done")

	return &WhileStmt{
		Condition: condition,
		Body:      body,
		Until:     until,
	}
}

func (p *parser) parseFor() Node {
	p.advance()

	if p.pos >= len(p.tokens) {
		return nil
	}

	if p.peek() == "((" {
		return p.parseCStyleFor()
	}

	varName := p.advance()

	var words []string
	if p.match("in") {
		for p.pos < len(p.tokens) && p.peek() != ";" && p.peek() != "do" {
			words = append(words, p.advance())
		}
	}

	if p.peek() == ";" {
		p.advance()
	}
	if !p.match("do") {
		return &ForStmt{Var: varName, Words: words}
	}

	body := p.parseCompoundBody("done")
	p.match("done")

	return &ForStmt{
		Var:   varName,
		Words: words,
		Body:  body,
	}
}

func (p *parser) parseCStyleFor() Node {
	p.advance()

	if p.pos >= len(p.tokens) {
		return nil
	}

	expr := strings.TrimSpace(p.advance())
	parts := strings.SplitN(expr, ";", 3)

	init := ""
	cond := ""
	step := ""
	if len(parts) >= 1 {
		init = strings.TrimSpace(parts[0])
	}
	if len(parts) >= 2 {
		cond = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		step = strings.TrimSpace(parts[2])
	}

	p.skipSemicolons()
	if !p.match("do") {
		return &CStyleForStmt{Init: init, Cond: cond, Step: step}
	}

	body := p.parseCompoundBody("done")
	p.match("done")

	return &CStyleForStmt{
		Init: init,
		Cond: cond,
		Step: step,
		Body: body,
	}
}

func (p *parser) parseSelect() Node {
	p.advance()

	if p.pos >= len(p.tokens) {
		return nil
	}

	varName := p.advance()

	var words []string
	if p.match("in") {
		for p.pos < len(p.tokens) && p.peek() != ";" && p.peek() != "do" {
			words = append(words, p.advance())
		}
	}

	if p.peek() == ";" {
		p.advance()
	}
	if !p.match("do") {
		return &SelectStmt{Var: varName, Words: words}
	}

	body := p.parseCompoundBody("done")
	p.match("done")

	return &SelectStmt{
		Var:   varName,
		Words: words,
		Body:  body,
	}
}

func (p *parser) isFuncDefPattern() bool {
	if p.pos >= len(p.tokens) {
		return false
	}
	t := p.peek()
	if isKeyword(t) {
		return false
	}
	if p.peekAt(1) == "(" && p.peekAt(2) == ")" {
		return isValidVarName(t)
	}
	if strings.HasSuffix(t, "(") && p.peekAt(1) == ")" {
		name := t[:len(t)-1]
		return isValidVarName(name)
	}
	return false
}

func (p *parser) parseFuncDef() Node {
	if p.peek() == "function" {
		p.advance()
	}

	if p.pos >= len(p.tokens) {
		return nil
	}

	name := p.advance()
	if strings.HasSuffix(name, "(") {
		name = name[:len(name)-1]
	} else {
		if p.peek() == "(" {
			p.advance()
		}
	}
	p.match(")")

	if !isValidVarName(name) {
		return nil
	}

	if !p.match("{") {
		return nil
	}

	body := p.parseCompoundBody("}")
	p.match("}")

	return &FuncDef{
		Name: name,
		Body: body,
	}
}

func (p *parser) parseCase() Node {
	p.advance()

	word := ""
	if p.pos < len(p.tokens) && p.peek() != "in" && !p.isCompoundEnd(p.peek()) {
		word = p.advance()
	}
	p.match("in")

	branches := []CaseBranch{}
	for p.pos < len(p.tokens) && p.peek() != "esac" {
		p.match(";")

		if p.peek() == "esac" {
			break
		}

		patterns := []string{}
		for p.pos < len(p.tokens) && p.peek() != ")" && p.peek() != "esac" {
			patterns = append(patterns, p.advance())
		}

		if p.match(")") {
			body := p.parseCaseBody()
			branches = append(branches, CaseBranch{Patterns: patterns, Body: body})
		}
	}

	p.match("esac")

	return &CaseStmt{
		Word:     word,
		Branches: branches,
	}
}

func (p *parser) parseCaseBody() Node {
	prog := &Program{}
	for p.pos < len(p.tokens) && p.peek() != ";;" && p.peek() != "esac" {
		if p.match(";") {
			continue
		}
		node := p.parseAndOr()
		if node != nil {
			prog.Commands = append(prog.Commands, node)
		}
	}
	p.match(";;")
	return prog
}

func (p *parser) parseCompoundBody(endTokens ...string) Node {
	prog := &Program{}
	endSet := make(map[string]bool)
	for _, t := range endTokens {
		endSet[t] = true
	}
	for p.pos < len(p.tokens) {
		if endSet[p.peek()] {
			break
		}
		if p.match(";") {
			continue
		}
		node := p.parseAndOr()
		if node != nil {
			prog.Commands = append(prog.Commands, node)
		}
	}
	if len(prog.Commands) == 0 {
		return nil
	}
	return prog
}

func (p *parser) parseSubshell() Node {
	p.advance()
	body := p.parseCompoundBody(")")
	p.match(")")
	return &Subshell{Body: body}
}

func (p *parser) parseGroup() Node {
	p.advance()
	body := p.parseCompoundBody("}")
	p.match("}")
	return &Group{Body: body}
}
